import type { Tab } from "./navigation.ts";
import { resetApprovalsBannerState, stopApprovalsBannerPolling } from "./app-approvals-banner.ts";
import { stopAppUpdateInstallPolling } from "./app-update.ts";
import type { OpenClawApp } from "./app.ts";
import { connectGateway } from "./app-gateway.ts";
import {
  startLogsPolling,
  startNodesPolling,
  stopLogsPolling,
  stopNodesPolling,
  startDebugPolling,
  stopDebugPolling,
} from "./app-polling.ts";
import {
  observeTopbar,
  resetChatScroll,
  scheduleChatScroll,
  scheduleLogsScroll,
} from "./app-scroll.ts";
import {
  applySettingsFromUrl,
  attachThemeListener,
  detachThemeListener,
  inferBasePath,
  syncTabWithLocation,
  syncThemeWithSettings,
} from "./app-settings.ts";

type LifecycleHost = {
  basePath: string;
  activeWorkspace: "agents" | "terminal" | "knowledge";
  tab: Tab;
  sessionKey: string;
  chatHasAutoScrolled: boolean;
  chatLoading: boolean;
  chatMessages: unknown[];
  chatToolMessages: unknown[];
  chatStream: string;
  logsAutoFollow: boolean;
  logsAtBottom: boolean;
  logsEntries: unknown[];
  popStateHandler: () => void;
  topbarObserver: ResizeObserver | null;
};

export function handleConnected(host: LifecycleHost) {
  host.basePath = inferBasePath();
  applySettingsFromUrl(host as unknown as Parameters<typeof applySettingsFromUrl>[0]);
  syncTabWithLocation(host as unknown as Parameters<typeof syncTabWithLocation>[0], true);
  syncThemeWithSettings(host as unknown as Parameters<typeof syncThemeWithSettings>[0]);
  attachThemeListener(host as unknown as Parameters<typeof attachThemeListener>[0]);
  window.addEventListener("popstate", host.popStateHandler);
  connectGateway(host as unknown as Parameters<typeof connectGateway>[0]);
  startNodesPolling(host as unknown as Parameters<typeof startNodesPolling>[0]);
  if (host.tab === "logs") {
    startLogsPolling(host as unknown as Parameters<typeof startLogsPolling>[0]);
  }
  if (host.tab === "debug") {
    startDebugPolling(host as unknown as Parameters<typeof startDebugPolling>[0]);
  }
}

type SetupWizardHost = LifecycleHost & {
  onboarding?: boolean;
  setupWizardActive: boolean;
  setupWizardStartTimer: number | null;
  maybeStartSetupWizard: () => void;
};

export function handleFirstUpdated(host: LifecycleHost) {
  observeTopbar(host as unknown as Parameters<typeof observeTopbar>[0]);
  const wizardHost = host as unknown as SetupWizardHost;
  if (wizardHost.setupWizardStartTimer != null) {
    window.clearTimeout(wizardHost.setupWizardStartTimer);
  }
  wizardHost.setupWizardStartTimer = window.setTimeout(() => {
    wizardHost.setupWizardStartTimer = null;
    wizardHost.maybeStartSetupWizard();
  }, 400);
}

export function handleDisconnected(host: LifecycleHost) {
  window.removeEventListener("popstate", host.popStateHandler);
  stopNodesPolling(host as unknown as Parameters<typeof stopNodesPolling>[0]);
  stopLogsPolling(host as unknown as Parameters<typeof stopLogsPolling>[0]);
  stopDebugPolling(host as unknown as Parameters<typeof stopDebugPolling>[0]);
  stopApprovalsBannerPolling(host as unknown as OpenClawApp);
  resetApprovalsBannerState(host as unknown as OpenClawApp);
  stopAppUpdateInstallPolling(host as unknown as Parameters<typeof stopAppUpdateInstallPolling>[0]);
  detachThemeListener(host as unknown as Parameters<typeof detachThemeListener>[0]);
  host.topbarObserver?.disconnect();
  host.topbarObserver = null;
}

function isChatShellTab(tab: Tab): boolean {
  return tab === "chat" || tab === "message";
}

export function handleUpdated(host: LifecycleHost, changed: Map<PropertyKey, unknown>) {
  const terminalWorkspaceActive = host.activeWorkspace === "terminal";
  if (
    (isChatShellTab(host.tab) || terminalWorkspaceActive) &&
    (changed.has("chatMessages") ||
      (terminalWorkspaceActive && changed.has("terminalAssistant")) ||
      changed.has("chatToolMessages") ||
      changed.has("chatStream") ||
      changed.has("chatLoading") ||
      changed.has("chatRunId") ||
      changed.has("chatRunPhase") ||
      changed.has("activeWorkspace") ||
      changed.has("tab") ||
      changed.has("sessionKey"))
  ) {
    const forcedByTab = changed.has("tab");
    const enteringTerminalWorkspace =
      changed.has("activeWorkspace") && terminalWorkspaceActive;
    const forcedByLoad =
      changed.has("chatLoading") && changed.get("chatLoading") === true && !host.chatLoading;
    const switchedSession = changed.has("sessionKey");
    // Opening or switching a session: treat like first paint so we scroll to newest messages.
    // Otherwise scheduleChatScroll(force) is ignored after chatHasAutoScrolled is true once.
    if (switchedSession || forcedByLoad || enteringTerminalWorkspace) {
      resetChatScroll(host as unknown as Parameters<typeof resetChatScroll>[0]);
    }
    scheduleChatScroll(
      host as unknown as Parameters<typeof scheduleChatScroll>[0],
      forcedByTab ||
        forcedByLoad ||
        switchedSession ||
        enteringTerminalWorkspace ||
        !host.chatHasAutoScrolled,
    );
  }
  if (
    host.tab === "logs" &&
    (changed.has("logsEntries") || changed.has("logsAutoFollow") || changed.has("tab"))
  ) {
    if (host.logsAutoFollow && host.logsAtBottom) {
      scheduleLogsScroll(
        host as unknown as Parameters<typeof scheduleLogsScroll>[0],
        changed.has("tab") || changed.has("logsAutoFollow"),
      );
    }
  }
}
