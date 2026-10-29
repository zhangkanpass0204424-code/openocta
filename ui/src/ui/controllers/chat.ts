import type { GatewayBrowserClient } from "../gateway.ts";
import type * as v0_9 from "@a2ui/web_core/v0_9";
import type { ChatSessionResources } from "../chat/chat-resources.ts";
import { chatResourcesPayload } from "../chat/chat-resources.ts";
import type { ChatAttachment } from "../ui-types.ts";
import { extractText, extractThinking } from "../chat/message-extract.ts";
import {
  dedupeA2UIMessages,
  filterA2UIMessagesForSurface,
  mergeA2UIIntoMessageContent,
  removeA2UISurfaceFromMessages,
  resetChatA2UISurfaces,
} from "../chat/a2ui-bridge.ts";
import { canonicalGatewaySessionKey, gatewaySessionKeysEqual } from "../sessions/session-key-utils.js";
import {
  applyBackgroundChatEvent,
  getSessionChatRunState,
  persistActiveSessionRunState,
  persistSessionRunState,
  switchChatSessionRunState,
} from "../chat/chat-session-runs.ts";
import { generateUUID } from "../uuid.ts";

export type ChatState = {
  client: GatewayBrowserClient | null;
  connected: boolean;
  sessionKey: string;
  chatLoading: boolean;
  chatMessages: unknown[];
  chatThinkingLevel: string | null;
  chatSending: boolean;
  chatMessage: string;
  chatAttachments: ChatAttachment[];
  chatRunId: string | null;
  chatRunPhase: "idle" | "thinking" | "tool" | "streaming";
  chatStream: string | null;
  chatReasoningStream: string | null;
  chatStreamStartedAt: number | null;
  chatA2UIMessages: unknown[];
  /** Runs that ended (error/aborted/final); late delta/turn events are ignored. */
  chatTerminalRunIds?: string[];
  /** Last run that ended with error; used to dedupe history reload on complete. */
  chatErrorRunId?: string | null;
  lastError: string | null;
};

const CHAT_TERMINAL_RUN_LIMIT = 24;

function clearActiveChatRunState(state: ChatState, runId?: string) {
  state.chatStream = null;
  state.chatReasoningStream = null;
  if (runId?.trim()) {
    markChatRunTerminal(state, runId);
  }
  state.chatRunId = null;
  state.chatRunPhase = "idle";
  state.chatStreamStartedAt = null;
  state.chatA2UIMessages = [];
  state.chatSending = false;
  resetChatA2UISurfaces();
}

function markChatRunTerminal(state: ChatState, runId: string) {
  const id = runId.trim();
  if (!id) {
    return;
  }
  const prev = state.chatTerminalRunIds ?? [];
  if (prev.includes(id)) {
    return;
  }
  state.chatTerminalRunIds = [...prev, id].slice(-CHAT_TERMINAL_RUN_LIMIT);
}

function isChatRunTerminal(state: ChatState, runId: string): boolean {
  const id = runId.trim();
  if (!id) {
    return false;
  }
  return (state.chatTerminalRunIds ?? []).includes(id);
}

function isA2UIOnlyAssistantContent(content: unknown): boolean {
  return (
    Array.isArray(content) &&
    content.length > 0 &&
    content.every(
      (part) =>
        part != null &&
        typeof part === "object" &&
        (part as Record<string, unknown>).type === "a2ui",
    )
  );
}

function a2uiContentBlock(a2ui: unknown): { type: "a2ui"; a2ui: unknown } {
  return { type: "a2ui", a2ui };
}

/** Merge streamed A2UI blocks into chat history so they survive final/complete + history reload. */
function persistA2UIBlocksToChatMessages(state: ChatState, blocks: unknown[]) {
  if (blocks.length === 0) {
    return;
  }
  const messages = state.chatMessages;
  const last = messages[messages.length - 1] as Record<string, unknown> | undefined;
  const timestamp =
    typeof last?.timestamp === "number" ? last.timestamp : Date.now();

  if (last?.role === "assistant") {
    const existing: Array<Record<string, unknown>> = [];
    if (Array.isArray(last.content)) {
      for (const part of last.content) {
        if (part != null && typeof part === "object") {
          existing.push(part as Record<string, unknown>);
        }
      }
    } else if (typeof last.content === "string" && last.content.trim()) {
      existing.push({ type: "text", text: last.content });
    }
    state.chatMessages = [
      ...messages.slice(0, -1),
      {
        ...last,
        content: mergeA2UIIntoMessageContent(existing, blocks),
        timestamp,
      },
    ];
    return;
  }

  state.chatMessages = [
    ...messages,
    {
      role: "assistant",
      content: blocks.map((block) => a2uiContentBlock(block)),
      timestamp,
    },
  ];
}

function applyA2UIChatEvent(state: ChatState, payload: ChatEventPayload) {
  if (payload.a2ui == null) {
    return;
  }
  state.chatA2UIMessages = dedupeA2UIMessages([...state.chatA2UIMessages, payload.a2ui]);
  state.chatRunPhase = "streaming";
  const runId = typeof payload.runId === "string" ? payload.runId.trim() : "";
  // Late A2UI after final: merge into the last assistant bubble instead of appending a duplicate row.
  if (runId && isChatRunTerminal(state, runId)) {
    persistA2UIBlocksToChatMessages(state, [payload.a2ui]);
  }
}

function mergeLiveA2UIIntoFinalMessage(state: ChatState, finalMessage: Record<string, unknown>) {
  const pending = state.chatA2UIMessages;
  if (pending.length === 0) {
    return finalMessage;
  }
  const content = Array.isArray(finalMessage.content) ? [...finalMessage.content] : [];
  return {
    ...finalMessage,
    content: mergeA2UIIntoMessageContent(content, pending),
  };
}

function trimTrailingA2UIOnlyMessages(messages: unknown[]): unknown[] {
  let trimmed = messages;
  while (trimmed.length > 0) {
    const last = trimmed[trimmed.length - 1] as Record<string, unknown> | undefined;
    if (last?.role === "assistant" && isA2UIOnlyAssistantContent(last.content)) {
      trimmed = trimmed.slice(0, -1);
    } else {
      break;
    }
  }
  return trimmed;
}

/** Submit an A2UI button/action and start a new agent run on the gateway. */
export async function dispatchA2UIActionFromChat(
  state: ChatState,
  action: v0_9.A2uiClientAction,
): Promise<boolean> {
  if (!state.client || !state.connected) {
    state.lastError = "未连接网关，无法提交操作";
    return false;
  }
  const idempotencyKey = generateUUID();
  const userAction = {
    name: action.name,
    surfaceId: action.surfaceId,
    sourceComponentId: action.sourceComponentId,
    timestamp: new Date().toISOString(),
    context: { ...action.context },
  };
  const surfaceId = action.surfaceId.trim();
  if (surfaceId) {
    state.chatMessages = removeA2UISurfaceFromMessages(state.chatMessages, surfaceId);
    state.chatA2UIMessages = filterA2UIMessagesForSurface(state.chatA2UIMessages, surfaceId);
  }

  state.chatSending = true;
  state.lastError = null;
  try {
    const res = await state.client.request<{ runId?: string }>("chat.a2ui.action", {
      sessionKey: canonicalGatewaySessionKey(state.sessionKey),
      userAction,
      idempotencyKey,
    });
    const runId =
      typeof res?.runId === "string" && res.runId.trim() !== "" ? res.runId.trim() : idempotencyKey;
    state.chatRunId = runId;
    state.chatErrorRunId = null;
  state.chatRunPhase = "thinking";
  state.chatStream = null;
  state.chatReasoningStream = null;
  state.chatStreamStartedAt = Date.now();
    state.chatA2UIMessages = [];
    resetChatA2UISurfaces();
    return true;
  } catch (err) {
    state.lastError = String(err);
    return false;
  } finally {
    state.chatSending = false;
  }
}

export { switchChatSessionRunState, isSessionChatBusy, persistActiveSessionRunState } from "../chat/chat-session-runs.ts";

export type ChatEventPayload = {
  runId: string;
  sessionKey: string;
  state: "delta" | "turn" | "final" | "complete" | "aborted" | "error" | "a2ui";
  /** Incremental streaming chunk (preferred over full snapshot in message.content). */
  text?: string;
  /** Incremental reasoning / thinking chunk from the model. */
  reasoning?: string;
  message?: unknown;
  a2ui?: unknown;
  errorMessage?: string;
};

/** Last N messages for chat.history and thread rendering (gateway hard-caps above this). */
export const CHAT_HISTORY_LIMIT = 500;

export async function readSessionAttachment(state: ChatState, path: string) {
  if (!state.client || !state.connected) {
    return null;
  }
  const trimmed = path.trim();
  if (!trimmed) {
    return null;
  }
  try {
    return await state.client.request<Record<string, unknown>>("chat.attachment.read", {
      sessionKey: canonicalGatewaySessionKey(state.sessionKey),
      path: trimmed,
    });
  } catch {
    return null;
  }
}

export async function loadChatHistory(state: ChatState) {
  if (!state.client || !state.connected) {
    return;
  }
  state.chatLoading = true;
  state.lastError = null;
  try {
    const res = await state.client.request<{ messages?: Array<unknown>; thinkingLevel?: string }>(
      "chat.history",
      {
        sessionKey: canonicalGatewaySessionKey(state.sessionKey),
        limit: CHAT_HISTORY_LIMIT,
      },
    );
    state.chatMessages = Array.isArray(res.messages) ? res.messages : [];
    state.chatThinkingLevel = res.thinkingLevel ?? null;
  } catch (err) {
    state.lastError = String(err);
  } finally {
    state.chatLoading = false;
  }
}

function dataUrlToBase64(dataUrl: string): { content: string; mimeType: string } | null {
  const match = /^data:([^;]+);base64,(.+)$/.exec(dataUrl);
  if (!match) {
    return null;
  }
  return { mimeType: match[1], content: match[2] };
}

function markSessionSendAcknowledged(sendSessionKey: string, state: ChatState) {
  const snap = getSessionChatRunState(sendSessionKey);
  persistSessionRunState(sendSessionKey, {
    ...snap,
    sending: false,
  });
  if (gatewaySessionKeysEqual(sendSessionKey, state.sessionKey)) {
    state.chatSending = false;
  }
}

export async function sendChatMessage(
  state: ChatState,
  message: string,
  attachments?: ChatAttachment[],
  modelRef?: string | null,
  resources?: ChatSessionResources,
  terminalSessionId?: string,
): Promise<string | null> {
  if (!state.client || !state.connected) {
    return null;
  }
  const msg = message.trim();
  const hasAttachments = attachments && attachments.length > 0;
  if (!msg && !hasAttachments) {
    return null;
  }

  const now = Date.now();
  const sendSessionKey = canonicalGatewaySessionKey(state.sessionKey);

  const isSessionReset = /^\/(?:new|reset)\b/i.test(msg);

  // Build user message content blocks
  const contentBlocks: Array<{ type: string; text?: string; source?: unknown }> = [];
  if (msg) {
    contentBlocks.push({ type: "text", text: msg });
  }
  // Add image/video previews to the message for display
  if (hasAttachments) {
    for (const att of attachments) {
      const kind =
        att.kind ??
        (att.mimeType?.startsWith("image/")
          ? "image"
          : att.mimeType?.startsWith("video/")
            ? "video"
            : "file");
      if (kind === "image") {
        contentBlocks.push({
          type: "image",
          source: { type: "base64", media_type: att.mimeType, data: att.dataUrl },
        });
      } else if (kind === "video") {
        contentBlocks.push({
          type: "text",
          text: `[视频] ${att.filename || "video"} (${att.mimeType || "video/mp4"})`,
        });
      } else {
        contentBlocks.push({
          type: "text",
          text: `[附件] ${att.filename || "file"} (${att.mimeType || "application/octet-stream"})`,
        });
      }
    }
  }

  state.chatMessages = [
    ...(isSessionReset ? [] : state.chatMessages),
    {
      role: "user",
      content: contentBlocks,
      timestamp: now,
    },
  ];

  state.chatSending = true;
  state.lastError = null;
  const runId = generateUUID();
  state.chatRunId = runId;
  state.chatErrorRunId = null;
  state.chatRunPhase = "thinking";
  state.chatStream = null;
  state.chatStreamStartedAt = now;
  state.chatA2UIMessages = [];
  resetChatA2UISurfaces();

  const priorSnap = getSessionChatRunState(sendSessionKey);
  persistSessionRunState(sendSessionKey, {
    runId,
    runPhase: "thinking",
    stream: null,
    reasoningStream: null,
    streamStartedAt: now,
    a2uiMessages: [],
    terminalRunIds: priorSnap.terminalRunIds,
    errorRunId: null,
    sending: true,
  });

  // Convert attachments to API format
  const apiAttachments = hasAttachments
    ? attachments
        .map((att) => {
          const parsed = dataUrlToBase64(att.dataUrl);
          if (!parsed) {
            return null;
          }
          const kind =
            att.kind ??
            (att.mimeType?.startsWith("image/")
              ? "image"
              : att.mimeType?.startsWith("video/")
                ? "video"
                : "file");
          return {
            type: kind,
            mimeType: parsed.mimeType,
            content: parsed.content,
            filename: att.filename,
            sizeBytes: att.sizeBytes,
          };
        })
        .filter((a): a is NonNullable<typeof a> => a !== null)
    : undefined;

  const trimmedModel = typeof modelRef === "string" ? modelRef.trim() : "";

  try {
    await state.client.request("chat.send", {
      terminalSessionId,
      sessionKey: sendSessionKey,
      message: msg,
      deliver: false,
      idempotencyKey: runId,
      attachments: apiAttachments,
      modelRef: trimmedModel || undefined,
      resources: chatResourcesPayload(
        resources ?? { configured: false, skillKeys: [], mcpServers: [], webSearch: true },
      ),
    });
    markSessionSendAcknowledged(sendSessionKey, state);
    return runId;
  } catch (err) {
    const error = String(err);
    const failedSnap = getSessionChatRunState(sendSessionKey);
    persistSessionRunState(sendSessionKey, {
      ...failedSnap,
      runId: null,
      runPhase: "idle",
      stream: null,
      reasoningStream: null,
      streamStartedAt: null,
      sending: false,
    });
    if (gatewaySessionKeysEqual(sendSessionKey, state.sessionKey)) {
      state.chatRunId = null;
      state.chatRunPhase = "idle";
      state.chatStream = null;
      state.chatReasoningStream = null;
      state.chatStreamStartedAt = null;
      state.lastError = error;
      state.chatMessages = [
        ...state.chatMessages,
        {
          role: "assistant",
          content: [{ type: "text", text: "Error: " + error }],
          timestamp: Date.now(),
        },
      ];
    } else {
      state.lastError = error;
    }
    return null;
  }
}

export async function abortChatRun(state: ChatState): Promise<boolean> {
  if (!state.client || !state.connected) {
    return false;
  }
  const runId = state.chatRunId;
  try {
    const sk = canonicalGatewaySessionKey(state.sessionKey);
    await state.client.request("chat.abort", runId ? { sessionKey: sk, runId } : { sessionKey: sk });
    // 网关会推送 chat/aborted，但若事件稍晚到达，先清本地状态以免「停止」后仍显示进行中且无法再次发送
    if (runId && state.chatRunId === runId) {
      state.chatRunId = null;
      state.chatRunPhase = "idle";
      state.chatStream = null;
      state.chatReasoningStream = null;
      state.chatStreamStartedAt = null;
    }
    return true;
  } catch (err) {
    state.lastError = String(err);
    return false;
  }
}

export function handleChatEvent(state: ChatState, payload?: ChatEventPayload) {
  if (!payload) {
    return null;
  }
  if (!gatewaySessionKeysEqual(payload.sessionKey, state.sessionKey)) {
    applyBackgroundChatEvent(payload.sessionKey, payload);
    return null;
  }

  const runId = typeof payload.runId === "string" ? payload.runId.trim() : "";

  // Final from another run (e.g. sub-agent announce): refresh history to show new message.
  // See https://github.com/openocta/openocta/issues/1909
  if (runId && state.chatRunId && runId !== state.chatRunId) {
    const stored = getSessionChatRunState(state.sessionKey);
    if (stored.runId === runId) {
      state.chatRunId = runId;
      state.chatStream = stored.stream;
      state.chatReasoningStream = stored.reasoningStream;
      state.chatRunPhase = stored.runPhase;
      state.chatStreamStartedAt = stored.streamStartedAt ?? state.chatStreamStartedAt;
      state.chatA2UIMessages = [...stored.a2uiMessages];
    } else if (payload.state === "final" || payload.state === "complete") {
      return payload.state;
    } else {
      return null;
    }
  }

  // Late A2UI after final/complete: still merge into history (common when final wins the race).
  if (payload.state === "a2ui" && payload.a2ui != null) {
    if (
      !runId ||
      !state.chatRunId ||
      state.chatRunId === runId ||
      isChatRunTerminal(state, runId)
    ) {
      applyA2UIChatEvent(state, payload);
      return "a2ui";
    }
    return null;
  }

  // Ignore stale streaming events after a run already ended (timeout/error/abort/final).
  if (runId && isChatRunTerminal(state, runId)) {
    if (payload.state === "complete") {
      if (state.chatRunId === runId) {
        clearActiveChatRunState(state, runId);
      }
      return "complete";
    }
    return null;
  }

  // After run ends locally, drop orphan delta/turn from the same run (keep a2ui — handled above).
  // Re-attach streaming after session switch (run state restored without local runId).
  if (runId && !state.chatRunId && (payload.state === "delta" || payload.state === "turn")) {
    state.chatRunId = runId;
    state.chatStreamStartedAt = state.chatStreamStartedAt ?? Date.now();
    state.chatRunPhase = payload.state === "turn" ? "tool" : "streaming";
  }

  if (payload.state === "delta") {
    if (state.chatA2UIMessages.length > 0) {
      state.chatRunPhase = "streaming";
      return "delta";
    }
    const reasoningChunk = typeof payload.reasoning === "string" ? payload.reasoning : "";
    if (reasoningChunk.length > 0) {
      const currentReasoning = state.chatReasoningStream ?? "";
      state.chatReasoningStream = currentReasoning + reasoningChunk;
      state.chatRunPhase = "thinking";
    }
    const chunk =
      typeof payload.text === "string"
        ? payload.text
        : payload.message != null
          ? extractText(payload.message)
          : null;
    if (typeof chunk === "string" && chunk.length > 0) {
      const current = state.chatStream ?? "";
      // Backward compat: older gateways sent full accumulated text each delta.
      if (current && chunk.length >= current.length && chunk.startsWith(current)) {
        state.chatStream = chunk;
      } else {
        state.chatStream = current + chunk;
      }
      state.chatRunPhase = "streaming";
    }
  } else if (payload.state === "turn") {
    if (payload.message && typeof payload.message === "object") {
      state.chatMessages = [...state.chatMessages, payload.message];
    }
    state.chatStream = null;
    state.chatA2UIMessages = [];
    resetChatA2UISurfaces();
    const turnThinking = extractThinking(payload.message)?.trim() ?? "";
    if (turnThinking) {
      state.chatReasoningStream = null;
    }
    state.chatRunPhase = "tool";
  } else if (payload.state === "final") {
    if (payload.message && typeof payload.message === "object") {
      const merged = mergeLiveA2UIIntoFinalMessage(
        state,
        payload.message as Record<string, unknown>,
      );
      state.chatMessages = [...trimTrailingA2UIOnlyMessages(state.chatMessages), merged];
    } else if (state.chatA2UIMessages.length > 0) {
      persistA2UIBlocksToChatMessages(state, state.chatA2UIMessages);
    }
    clearActiveChatRunState(state, runId);
  } else if (payload.state === "aborted") {
    clearActiveChatRunState(state, runId);
  } else if (payload.state === "complete") {
    clearActiveChatRunState(state, runId);
  } else if (payload.state === "error") {
    if (runId) {
      state.chatErrorRunId = runId;
    }
    clearActiveChatRunState(state, runId);
    // Error text is appended to transcript; reload history instead of duplicating in callout.
    state.lastError = null;
  }
  persistActiveSessionRunState(state);
  return payload.state;
}
