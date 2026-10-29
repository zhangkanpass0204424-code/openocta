import type { EventLogEntry } from "./app-events.ts";
import type { OpenClawApp } from "./app.ts";
import type { ExecApprovalRequest } from "./controllers/exec-approval.ts";
import type { GatewayEventFrame, GatewayHelloOk } from "./gateway.ts";
import type { Tab } from "./navigation.ts";
import type { UiSettings } from "./storage.ts";
import type { AgentsListResult, PresenceEntry, HealthSnapshot, StatusSummary } from "./types.ts";
import type { ChatAttachment, ChatQueueItem } from "./ui-types.ts";
import { CHAT_SESSIONS_ACTIVE_MINUTES, flushChatQueueForEvent } from "./app-chat.ts";
import { scheduleChatScroll } from "./app-scroll.ts";
import { defaultChatSessionResources, resetChatResourcesPanelUi } from "./chat/chat-resources.ts";
import {
  applySettings,
  loadCron,
  refreshActiveTab,
  setLastActiveSessionKey,
} from "./app-settings.ts";
import {
  handleAgentEvent,
  resetToolStream,
  type AgentEventPayload,
} from "./app-tool-stream.ts";
import { maybeRunDailyAppUpdateCheck } from "./app-update.ts";
import { loadAgents } from "./controllers/agents.ts";
import { loadAssistantIdentity } from "./controllers/assistant-identity.ts";
import { loadChatHistory } from "./controllers/chat.ts";
import { handleChatEvent, switchChatSessionRunState, type ChatEventPayload } from "./controllers/chat.ts";
import { gatewaySessionKeysEqual } from "./sessions/session-key-utils.js";
import { loadDevices } from "./controllers/devices.ts";
import {
  addExecApproval,
  parseExecApprovalRequested,
  parseExecApprovalResolved,
  removeExecApproval,
} from "./controllers/exec-approval.ts";
import {
  resetApprovalsBannerState,
  runApprovalsBannerPoll,
  startApprovalsBannerPolling,
  stopApprovalsBannerPolling,
} from "./app-approvals-banner.ts";
import { loadConfigSchema } from "./controllers/config.ts";
import { loadNodes } from "./controllers/nodes.ts";
import { loadSessions } from "./controllers/sessions.ts";
import { gatewayWebSocketUrl } from "./gateway-url.ts";
import { setGatewayHttpCredential } from "./gateway-http-auth.ts";
import { GatewayBrowserClient } from "./gateway.ts";

type GatewayHost = {
  terminalAssistant?: import("./terminal-assistant-state.ts").TerminalAssistantController;
  settings: UiSettings;
  password: string;
  client: GatewayBrowserClient | null;
  connected: boolean;
  hello: GatewayHelloOk | null;
  lastError: string | null;
  gatewayConnectionPending: boolean;
  gatewayAuthRequired: boolean;
  gatewayAuthError: string | null;
  onboarding?: boolean;
  eventLogBuffer: EventLogEntry[];
  eventLog: EventLogEntry[];
  tab: Tab;
  presenceEntries: PresenceEntry[];
  presenceError: string | null;
  presenceStatus: StatusSummary | null;
  agentsLoading: boolean;
  agentsList: AgentsListResult | null;
  agentsError: string | null;
  debugHealth: HealthSnapshot | null;
  assistantName: string;
  assistantAvatar: string | null;
  assistantAgentId: string | null;
  sessionKey: string;
  chatMessage: string;
  chatAttachments: ChatAttachment[];
  chatAttachmentError: string | null;
  chatModelRef: string | null;
  chatQueue: ChatQueueItem[];
  chatRunId: string | null;
  refreshSessionsAfterChat: Set<string>;
  execApprovalQueue: ExecApprovalRequest[];
  execApprovalError: string | null;
};

type SessionDefaultsSnapshot = {
  defaultAgentId?: string;
  mainKey?: string;
  mainSessionKey?: string;
  scope?: string;
};

function normalizeSessionKeyForDefaults(
  value: string | undefined,
  defaults: SessionDefaultsSnapshot,
): string {
  const raw = (value ?? "").trim();
  const mainSessionKey = defaults.mainSessionKey?.trim();
  if (!mainSessionKey) {
    return raw;
  }
  if (!raw) {
    return mainSessionKey;
  }
  const mainKey = defaults.mainKey?.trim() || "main";
  const defaultAgentId = defaults.defaultAgentId?.trim();
  const isAlias =
    raw === "main" ||
    raw === mainKey ||
    (defaultAgentId &&
      (raw === `agent:${defaultAgentId}:main` || raw === `agent:${defaultAgentId}:${mainKey}`));
  return isAlias ? mainSessionKey : raw;
}

function applySessionDefaults(host: GatewayHost, defaults?: SessionDefaultsSnapshot) {
  if (!defaults?.mainSessionKey) {
    return;
  }
  const resolvedSessionKey = normalizeSessionKeyForDefaults(host.sessionKey, defaults);
  const resolvedSettingsSessionKey = normalizeSessionKeyForDefaults(
    host.settings.sessionKey,
    defaults,
  );
  const resolvedLastActiveSessionKey = normalizeSessionKeyForDefaults(
    host.settings.lastActiveSessionKey,
    defaults,
  );
  const nextSessionKey = resolvedSessionKey || resolvedSettingsSessionKey || host.sessionKey;
  const nextSettings = {
    ...host.settings,
    sessionKey: resolvedSettingsSessionKey || nextSessionKey,
    lastActiveSessionKey: resolvedLastActiveSessionKey || nextSessionKey,
  };
  const shouldUpdateSettings =
    nextSettings.sessionKey !== host.settings.sessionKey ||
    nextSettings.lastActiveSessionKey !== host.settings.lastActiveSessionKey;
  if (nextSessionKey !== host.sessionKey) {
    switchChatSessionRunState(host as unknown as OpenClawApp, nextSessionKey);
    host.chatMessage = "";
    host.chatAttachments = [];
    host.chatAttachmentError = null;
    host.chatModelRef = null;
    (host as unknown as { chatResources: ReturnType<typeof defaultChatSessionResources> }).chatResources =
      defaultChatSessionResources();
    resetChatResourcesPanelUi(
      host as unknown as Parameters<typeof resetChatResourcesPanelUi>[0],
    );
    host.chatQueue = [];
    resetToolStream(host as unknown as Parameters<typeof resetToolStream>[0]);
  }
  if (shouldUpdateSettings) {
    applySettings(host as unknown as Parameters<typeof applySettings>[0], nextSettings);
  }
}

export function connectGateway(host: GatewayHost) {
  host.gatewayConnectionPending = true;
  setGatewayHttpCredential(
    host.settings.gatewayUrl,
    host.password.trim() ? host.password : host.settings.token,
  );
  host.lastError = null;
  host.hello = null;
  host.connected = false;
  host.execApprovalQueue = [];
  host.execApprovalError = null;

  host.client?.stop();
  host.client = new GatewayBrowserClient({
    url: gatewayWebSocketUrl(host.settings.gatewayUrl),
    token: host.settings.token.trim() ? host.settings.token : undefined,
    password: host.password.trim() ? host.password : undefined,
    clientName: "openclaw-control-ui",
    mode: "webchat",
    onAuthRequired: (message) => {
      host.gatewayConnectionPending = false;
      host.gatewayAuthRequired = true;
      host.gatewayAuthError = /未配置/.test(message)
        ? "网关已启用密码登录，但尚未设置密码。请管理员先在配置中设置 gateway.auth.password。"
        : host.password.trim()
          ? "密码或令牌不正确，请重试。"
          : null;
    },
    onHello: (hello) => {
      host.connected = true;
      host.gatewayConnectionPending = false;
      host.lastError = null;
      host.gatewayAuthRequired = false;
      host.gatewayAuthError = null;
      host.hello = hello;
      applySnapshot(host, hello);
      // Reset orphaned chat run state from before disconnect.
      // Any in-flight run's final event was lost during the disconnect window.
      host.chatRunId = null;
      (host as unknown as { chatTerminalRunIds: string[] }).chatTerminalRunIds = [];
      (host as unknown as { chatErrorRunId: string | null }).chatErrorRunId = null;
      (host as unknown as { chatRunPhase: "idle" | "thinking" | "tool" | "streaming" }).chatRunPhase =
        "idle";
      (host as unknown as { chatStream: string | null }).chatStream = null;
      (host as unknown as { chatReasoningStream: string | null }).chatReasoningStream = null;
      (host as unknown as { chatStreamStartedAt: number | null }).chatStreamStartedAt = null;
      resetToolStream(host as unknown as Parameters<typeof resetToolStream>[0]);
      void loadConfigSchema(host as unknown as OpenClawApp);
      void loadAssistantIdentity(host as unknown as OpenClawApp);
      void loadAgents(host as unknown as OpenClawApp);
      void loadNodes(host as unknown as OpenClawApp, { quiet: true });
      void loadDevices(host as unknown as OpenClawApp, { quiet: true });
      void refreshActiveTab(host as unknown as Parameters<typeof refreshActiveTab>[0]);
      const app = host as unknown as OpenClawApp;
      resetApprovalsBannerState(app);
      void (async () => {
        await runApprovalsBannerPoll(app);
        startApprovalsBannerPolling(app);
      })();
      void maybeRunDailyAppUpdateCheck(app as unknown as import("./app-view-state.ts").AppViewState);
    },
    onClose: ({ code, reason }) => {
      host.connected = false;
      host.gatewayConnectionPending = code === 1012;
      stopApprovalsBannerPolling(host as unknown as OpenClawApp);
      resetApprovalsBannerState(host as unknown as OpenClawApp);
      // Code 1012 = Service Restart (expected during config saves, don't show as error)
      if (code !== 1012) {
        const r = (reason ?? "").trim();
        if (code === 4008 && /认证失败|invalid_gateway_token/i.test(r)) {
          host.gatewayAuthRequired = true;
          host.gatewayAuthError = /未配置/.test(r)
            ? "网关已启用密码登录，但尚未设置密码。请管理员先在配置中设置 gateway.auth.password。"
            : host.password.trim()
              ? "密码或令牌不正确，请重试。"
              : null;
        }
        if (r) {
          host.lastError = r;
        } else if (code === 1006) {
          host.lastError = "连接失败，请检查后端地址和网络连接";
        } else {
          host.lastError = `连接断开 (${code})`;
        }
      }
    },
    onEvent: (evt) => handleGatewayEvent(host, evt),
    onGap: ({ expected, received }) => {
      // Sequence gap is expected when client is a slow consumer or reconnects.
      // Log warning but don't show as error to avoid alarming users.
      console.warn(`[gateway] event gap detected (expected seq ${expected}, got ${received})`);
    },
  });
  host.client.start();
}

export function handleGatewayEvent(host: GatewayHost, evt: GatewayEventFrame) {
  try {
    handleGatewayEventUnsafe(host, evt);
  } catch (err) {
    console.error("[gateway] handleGatewayEvent error:", evt.event, err);
  }
}

function handleGatewayEventUnsafe(host: GatewayHost, evt: GatewayEventFrame) {
  host.eventLogBuffer = [
    { ts: Date.now(), event: evt.event, payload: evt.payload },
    ...host.eventLogBuffer,
  ].slice(0, 250);
  if (host.tab === "debug") {
    host.eventLog = host.eventLogBuffer;
  }

  if (evt.event === "agent") {
    if (host.onboarding) {
      return;
    }
    const agentPayload = evt.payload as AgentEventPayload | undefined;
    if (host.terminalAssistant?.receiveAgent(agentPayload?.sessionKey, agentPayload?.data?.phase as string | undefined)) return;
    handleAgentEvent(
      host as unknown as Parameters<typeof handleAgentEvent>[0],
      agentPayload,
    );
    // 工具开始执行时清掉上一轮残留的流式文本，保持运行中状态以显示加载指示
    if (agentPayload?.stream === "tool" && agentPayload?.data?.phase === "start") {
      const eventSessionKey =
        typeof agentPayload.sessionKey === "string" ? agentPayload.sessionKey : undefined;
      if (
        eventSessionKey &&
        !gatewaySessionKeysEqual(eventSessionKey, host.sessionKey)
      ) {
        return;
      }
      (host as unknown as { chatRunPhase: "idle" | "thinking" | "tool" | "streaming" }).chatRunPhase =
        "tool";
    }
    return;
  }

  if (evt.event === "chat") {
    const payload = evt.payload as ChatEventPayload | undefined;
    if (host.terminalAssistant) {
      host.terminalAssistant.syncConnection(host.client, host.connected);
      if (host.terminalAssistant.receive(payload)) return;
    }
    if (payload?.sessionKey) {
      setLastActiveSessionKey(
        host as unknown as Parameters<typeof setLastActiveSessionKey>[0],
        payload.sessionKey,
      );
    }
    const state = handleChatEvent(host as unknown as OpenClawApp, payload);
    if (state === "delta") {
      scheduleChatScroll(host as unknown as Parameters<typeof scheduleChatScroll>[0], true);
    }
    if (state === "a2ui" || state === "turn") {
      scheduleChatScroll(host as unknown as Parameters<typeof scheduleChatScroll>[0], true);
    }
    if (state === "final" || state === "complete" || state === "error" || state === "aborted") {
      resetToolStream(host as unknown as Parameters<typeof resetToolStream>[0]);
      void flushChatQueueForEvent(host as unknown as Parameters<typeof flushChatQueueForEvent>[0]);
      const runId = payload?.runId;
      if (runId && host.refreshSessionsAfterChat.has(runId)) {
        host.refreshSessionsAfterChat.delete(runId);
        if (state === "final" || state === "complete") {
          void loadSessions(host as unknown as OpenClawApp, {
            activeMinutes: CHAT_SESSIONS_ACTIVE_MINUTES,
            includeLastMessage: true,
          });
        }
      }
    }
    const app = host as unknown as OpenClawApp;
    const errorRunId = app.chatErrorRunId ?? null;
    const payloadRunId = typeof payload?.runId === "string" ? payload.runId.trim() : "";
    const skipHistoryReload =
      state === "complete" && Boolean(errorRunId && payloadRunId && errorRunId === payloadRunId);
    if (state === "error") {
      void loadChatHistory(app);
    } else if ((state === "final" || state === "complete" || state === "aborted") && !skipHistoryReload) {
      void loadChatHistory(app);
    }
    if (state === "complete" && skipHistoryReload) {
      app.chatErrorRunId = null;
    }
    return;
  }

  if (evt.event === "presence") {
    const payload = evt.payload as { presence?: PresenceEntry[] } | undefined;
    if (payload?.presence && Array.isArray(payload.presence)) {
      host.presenceEntries = payload.presence;
      host.presenceError = null;
      host.presenceStatus = null;
    }
    return;
  }

  if (evt.event === "cron" && host.tab === "cron") {
    void loadCron(host as unknown as Parameters<typeof loadCron>[0]);
  }

  if (evt.event === "device.pair.requested" || evt.event === "device.pair.resolved") {
    void loadDevices(host as unknown as OpenClawApp, { quiet: true });
  }

  if (evt.event === "exec.approval.requested") {
    const entry = parseExecApprovalRequested(evt.payload);
    if (entry) {
      host.execApprovalQueue = addExecApproval(host.execApprovalQueue, entry);
      host.execApprovalError = null;
      const delay = Math.max(0, entry.expiresAtMs - Date.now() + 500);
      window.setTimeout(() => {
        host.execApprovalQueue = removeExecApproval(host.execApprovalQueue, entry.id);
      }, delay);
    }
    return;
  }

  if (evt.event === "exec.approval.resolved") {
    const resolved = parseExecApprovalResolved(evt.payload);
    if (resolved) {
      host.execApprovalQueue = removeExecApproval(host.execApprovalQueue, resolved.id);
    }
  }

  if (evt.event === "swarm.member.updated") {
    const app = host as unknown as OpenClawApp;
    if (app.tab !== "agentSwarm") {
      return;
    }
    void import("./controllers/swarm.ts").then(({ applySwarmMemberUpdated }) => {
      const member = (evt.payload as { member?: import("./types/swarm-types.ts").SwarmMember })?.member;
      if (member) {
        applySwarmMemberUpdated(app, member);
      }
    });
    return;
  }

  if (evt.event === "swarm.task.delta") {
    const app = host as unknown as OpenClawApp;
    if (app.tab !== "agentSwarm") {
      return;
    }
    void import("./controllers/swarm.ts").then(({ applySwarmTaskDelta }) => {
      applySwarmTaskDelta(
        app,
        (evt.payload ?? {}) as {
          memberId?: string;
          stream?: string;
          data?: Record<string, unknown>;
        },
      );
    });
    return;
  }

  if (evt.event === "swarm.task.final") {
    const app = host as unknown as OpenClawApp;
    if (app.tab !== "agentSwarm") {
      return;
    }
    void import("./controllers/swarm.ts").then(({ applySwarmTaskFinal }) => {
      void applySwarmTaskFinal(app);
    });
  }
}

export function applySnapshot(host: GatewayHost, hello: GatewayHelloOk) {
  const snapshot = hello.snapshot as
    | {
        presence?: PresenceEntry[];
        health?: HealthSnapshot;
        sessionDefaults?: SessionDefaultsSnapshot;
      }
    | undefined;
  if (snapshot?.presence && Array.isArray(snapshot.presence)) {
    host.presenceEntries = snapshot.presence;
  }
  if (snapshot?.health) {
    host.debugHealth = snapshot.health;
  }
  if (snapshot?.sessionDefaults) {
    applySessionDefaults(host, snapshot.sessionDefaults);
  }
}
