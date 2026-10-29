import type { ReactiveController, ReactiveControllerHost } from "lit";
import type { GatewayBrowserClient } from "./gateway.ts";
import { abortChatRun, handleChatEvent, loadChatHistory, sendChatMessage } from "./controllers/chat.ts";
import type { ChatEventPayload, ChatState } from "./controllers/chat.ts";
import { defaultChatSessionResources } from "./chat/chat-resources.ts";
import type { ChatSessionResources } from "./chat/chat-resources.ts";
import { generateUUID } from "./uuid.ts";

export type TerminalConversation = ChatState & {
  modelRef: string | null;
  resources: ChatSessionResources;
  attachmentError: string | null;
};

export class TerminalAssistantController implements ReactiveController {
  private conversations = new Map<string, TerminalConversation>();
  private sessions = new Map<string, TerminalConversation>();
  private activeHostId = "";

  constructor(private readonly host: ReactiveControllerHost & { requestUpdate(name?: PropertyKey, oldValue?: unknown): void }) {
    host.addController(this);
  }

  get active(): TerminalConversation { return this.forHost(this.activeHostId); }

  selectHost(hostId: string | null) {
    this.activeHostId = hostId ?? "";
  }

  notify() { this.host.requestUpdate("terminalAssistant", null); }

  forHost(hostId: string): TerminalConversation {
    let state = this.conversations.get(hostId);
    if (!state) {
      state = {
        client: null, connected: false, sessionKey: `custom:terminal-${generateUUID()}`,
        chatLoading: false, chatMessages: [], chatThinkingLevel: null, chatSending: false,
        chatMessage: "", chatAttachments: [], chatRunId: null, chatRunPhase: "idle",
        chatStream: null, chatReasoningStream: null, chatStreamStartedAt: null,
        chatA2UIMessages: [], lastError: null, modelRef: null,
        resources: defaultChatSessionResources(), attachmentError: null,
      };
      this.conversations.set(hostId, state);
      this.sessions.set(state.sessionKey, state);
    }
    return state;
  }

  syncConnection(client: GatewayBrowserClient | null, connected: boolean) {
    for (const state of this.sessions.values()) {
      state.client = client;
      state.connected = connected;
    }
    this.active.client = client;
    this.active.connected = connected;
  }

  async send(message: string, terminalSessionId?: string): Promise<boolean> {
    const state = this.active;
    if (!state.connected || state.chatRunId || state.chatSending || state.chatLoading) return false;
    const attachments = state.chatAttachments;
    state.chatAttachments = [];
    const pending = sendChatMessage(state, message, attachments, state.modelRef, state.resources, terminalSessionId);
    this.notify();
    const runId = await pending;
    if (!runId) state.chatAttachments = attachments;
    this.notify();
    return Boolean(runId);
  }

  async stop() {
    const state = this.active;
    await abortChatRun(state);
    this.notify();
  }

  async newConversation() {
    const key = this.activeHostId;
    const current = this.active;
    if (current.chatRunId || current.chatSending) return;
    this.conversations.delete(key);
    this.syncConnection(current.client, current.connected);
    this.notify();
  }

  async reset() {
    const state = this.active;
    if (state.chatRunId || state.chatSending || state.chatLoading) return;
    await this.send("/reset");
  }

  owns(sessionKey: string | undefined) {
    return Boolean(sessionKey && this.sessions.has(sessionKey));
  }

  receiveAgent(sessionKey: string | undefined, phase: string | undefined): boolean {
    const state = sessionKey ? this.sessions.get(sessionKey) : undefined;
    if (!state) return false;
    if (phase === "start" && state.chatRunId) state.chatRunPhase = "tool";
    this.notify();
    return true;
  }

  receive(payload: ChatEventPayload | undefined): boolean {
    if (!payload) return false;
    const state = this.sessions.get(payload.sessionKey);
    if (!state) return false;
    const result = handleChatEvent(state, payload);
    this.notify();
    if (result === "error") state.lastError = "终端 AI 请求失败，请重试或查看网关日志。";
    if (["final", "complete", "aborted", "error"].includes(result ?? "")) {
      void this.refresh(state);
    }
    return true;
  }

  private async refresh(state: TerminalConversation) {
    const error = state.lastError;
    await loadChatHistory(state);
    if (error && !state.lastError) state.lastError = error;
    this.notify();
  }

  hostDisconnected() {
    this.conversations.clear();
    this.sessions.clear();
  }
}
