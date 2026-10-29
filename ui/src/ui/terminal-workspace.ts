import { terminalAssistantView } from "./terminal-assistant-view.ts";
import { html, nothing } from "lit";
import { unsafeHTML } from "lit/directives/unsafe-html.js";
import { icons } from "./icons.ts";
import type { AppViewState } from "./app-view-state.ts";
import { toSanitizedMarkdownHtml } from "./markdown.ts";
import { gatewayHttpBase } from "./gateway-url.ts";
import { loadAttachmentFromFile, validateChatAttachmentFile } from "./views/chat.ts";
import { extractA2UIDisplayText } from "./chat/a2ui-bridge.ts";

type TerminalHost = AppViewState["terminalHosts"][number];

function clearTerminalCredentials(state: AppViewState) {
  state.terminalPasswordInput = "";
  state.terminalPrivateKeyInput = "";
  state.terminalPassphraseInput = "";
}

function exportConversation(state: AppViewState) {
  const transcript = (state.chatMessages ?? [])
    .map((message) => {
      const text = textFromMessage(message);
      if (!text) return "";
      const role = roleFromMessage(message) === "user" ? "用户" : "助手";
      const timestamp = formatTimestamp(message);
      return `### ${role}${timestamp ? ` · ${timestamp}` : ""}\n\n${text}`;
    })
    .filter(Boolean);
  if (state.chatStream) {
    transcript.push(`### 助手 · 正在生成\n\n${state.chatStream}`);
  }
  if (!transcript.length) {
    state.terminalFeedback = "当前没有可导出的对话内容。";
    return;
  }

  const blob = new Blob([transcript.join("\n\n")], { type: "text/markdown;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `openocta-terminal-chat-${new Date().toISOString().slice(0, 10)}.md`;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
  state.terminalFeedback = "对话已导出为 Markdown 文件。";
}

function openTerminalHostDialog(state: AppViewState, host?: TerminalHost) {
  state.terminalError = null;
  state.terminalHostInput = host?.host ?? "";
  state.terminalPortInput = String(host?.port ?? 22);
  state.terminalUserInput = host?.username ?? "root";
  state.terminalHostFingerprint = host?.fingerprint ?? "";
  state.terminalHostKeyConfirmed = Boolean(host?.fingerprint);
  clearTerminalCredentials(state);
  state.terminalHostDialogOpen = true;
}

function closeTerminalHostDialog(state: AppViewState) {
  state.terminalHostDialogOpen = false;
  state.terminalHostFingerprint = "";
  state.terminalHostKeyConfirmed = false;
  state.terminalError = null;
  clearTerminalCredentials(state);
}

async function connectTerminalHost(state: AppViewState) {
  if (state.terminalConnecting) return;
  const host = state.terminalHostInput.trim();
  const port = Number(state.terminalPortInput);
  const username = state.terminalUserInput.trim() || "root";
  if (!host || !Number.isInteger(port) || port < 1 || port > 65535) {
    state.terminalError = "请填写有效的主机地址和端口（1–65535）";
    return;
  }
  const existing = state.terminalHosts.find(
    (item) => item.host === host && item.port === port && item.username === username,
  );
  const fingerprint = state.terminalHostKeyConfirmed ? state.terminalHostFingerprint : "";
  if (fingerprint && !state.terminalPasswordInput && !state.terminalPrivateKeyInput) {
    state.terminalError = "请输入 SSH 密码或私钥";
    return;
  }
  const apiBase = gatewayHttpBase(state.settings.gatewayUrl);
  if (!apiBase) {
    state.terminalError = "未配置 Gateway 地址，无法连接 SSH";
    return;
  }

  state.terminalConnecting = true;
  state.terminalError = null;
  try {
    const response = await fetch(`${apiBase}/api/terminal/sessions`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        host,
        port,
        username,
        ...(fingerprint
          ? {
              hostKeyFingerprint: fingerprint,
              ...(state.terminalPasswordInput ? { password: state.terminalPasswordInput } : {}),
              ...(state.terminalPrivateKeyInput ? { privateKey: state.terminalPrivateKeyInput } : {}),
              ...(state.terminalPassphraseInput ? { passphrase: state.terminalPassphraseInput } : {}),
            }
          : {}),
      }),
    });
    const result = (await response.json().catch(() => ({}))) as {
      code?: string;
      fingerprint?: string;
      id?: string;
      sessionId?: string;
    };
    if (response.status === 412 && result.code === "host_key_confirmation_required") {
      state.terminalHostFingerprint = result.fingerprint ?? "";
      state.terminalHostKeyConfirmed = false;
      clearTerminalCredentials(state);
      state.terminalError = "请核对服务器指纹；确认信任后再输入 SSH 凭据。";
      return;
    }
    if (response.status === 409 && result.code === "host_key_mismatch") {
      state.terminalHostFingerprint = "";
      state.terminalHostKeyConfirmed = false;
      state.terminalError = "服务器指纹已变化，已停止连接。请重新核对新指纹。";
      return;
    }
    if (!response.ok || !result.id || !result.sessionId) {
      const messages: Record<string, string> = {
        invalid_request: "连接参数无效；请检查主机、端口、用户名和认证凭据。",
        ssh_connection_failed: "SSH 连接失败；请检查网络、账号、密码或私钥。",
        session_capacity_reached: "SSH 会话数量已达上限，请先关闭其他会话。",
        terminal_store_unavailable: "Gateway 主机列表存储不可用，请检查服务日志。",
        terminal_host_save_failed: "主机已连接，但保存到 Gateway 主机列表失败。",
      };
      throw new Error(messages[result.code ?? ""] ?? `SSH 连接失败（HTTP ${response.status}）`);
    }

    const entry: TerminalHost = {
      id: result.id,
      host,
      port,
      username,
      connected: true,
      sessionId: result.sessionId,
      fingerprint,
    };
    state.terminalHosts = existing
      ? state.terminalHosts.map((item) => (item.id === existing.id ? entry : item))
      : [...state.terminalHosts, entry];
    state.terminalActiveHostId = entry.id;
    state.terminalSessionError = null;
    state.terminalHostDialogOpen = false;
    state.terminalHostFingerprint = "";
    state.terminalHostKeyConfirmed = false;
    state.terminalError = null;
  } catch (error) {
    state.terminalError = error instanceof Error ? error.message : "SSH 连接失败";
  } finally {
    clearTerminalCredentials(state);
    state.terminalConnecting = false;
  }
}

async function disconnectTerminalHost(state: AppViewState, host: TerminalHost) {
  if (!host.sessionId) return true;
  try {
    const apiBase = gatewayHttpBase(state.settings.gatewayUrl);
    if (!apiBase) throw new Error("未配置 Gateway 地址，无法关闭 SSH 会话");
    const response = await fetch(
      `${apiBase}/api/terminal/sessions/${encodeURIComponent(host.sessionId)}`,
      { method: "DELETE" },
    );
    if (!response.ok && response.status !== 404) {
      throw new Error(`关闭 SSH 会话失败（HTTP ${response.status}）`);
    }
    state.terminalHosts = state.terminalHosts.map((item) =>
      item.id === host.id ? { ...item, connected: false, sessionId: null } : item,
    );
    state.terminalSessionError = null;
    return true;
  } catch (error) {
    console.error("[terminal] failed to close SSH session:", error);
    state.terminalSessionError = error instanceof Error ? error.message : "关闭 SSH 会话失败";
    return false;
  }
}

async function removeTerminalHost(state: AppViewState, host: TerminalHost) {
  if (!(await disconnectTerminalHost(state, host))) return;
  try {
    const apiBase = gatewayHttpBase(state.settings.gatewayUrl);
    if (!apiBase) throw new Error("未配置 Gateway 地址，无法移除 SSH 主机");
    const response = await fetch(
      `${apiBase}/api/terminal/hosts/${encodeURIComponent(host.id)}`,
      { method: "DELETE" },
    );
    if (!response.ok && response.status !== 404) {
      throw new Error(`移除 SSH 主机失败（HTTP ${response.status}）`);
    }
    state.terminalHosts = state.terminalHosts.filter((item) => item.id !== host.id);
    if (state.terminalActiveHostId === host.id) {
      state.terminalActiveHostId = state.terminalHosts[0]?.id ?? null;
    }
    state.terminalSessionError = null;
  } catch (error) {
    console.error("[terminal] failed to remove SSH host:", error);
    state.terminalSessionError = error instanceof Error ? error.message : "移除 SSH 主机失败";
  }
}

async function sendTerminalCommand(
  state: AppViewState,
  command: string,
  targetHostId: string | null,
) {
  const host = state.terminalWorkspace.resolveCommandTarget();
  if (!host || host.id !== targetHostId || state.terminalWorkspace.command.text !== command) {
    state.terminalFeedback = "命令目标未连接或会话已变化，请重新绑定并检查命令后执行。";
    return;
  }
  try {
    const apiBase = gatewayHttpBase(state.settings.gatewayUrl);
    if (!apiBase) throw new Error("未配置 Gateway 地址，无法发送终端命令");
    const proposal = state.terminalWorkspace.command;
    const response = await fetch(
      `${apiBase}/api/terminal/sessions/${encodeURIComponent(host.sessionId!)}/input`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ data: `${command}\r` }),
      },
    );
    if (!response.ok) throw new Error(`发送命令失败（HTTP ${response.status}）`);
    if (state.terminalWorkspace.command === proposal) state.terminalWorkspace.clearCommand();
    state.terminalFeedback = "命令已发送到 SSH 终端。";
  } catch (error) {
    console.error("[terminal] failed to send command:", error);
    state.terminalFeedback = error instanceof Error ? error.message : "发送命令失败";
  }
}

function handleTerminalAttachmentPick(state: AppViewState, event: Event) {
  const input = event.currentTarget;
  if (!(input instanceof HTMLInputElement)) return;
  const file = input.files?.[0];
  if (!file) return;

  state.chatAttachments = [];
  const validation = validateChatAttachmentFile(file);
  if (!validation.ok) {
    state.chatAttachmentError = validation.message;
    input.value = "";
    return;
  }

  state.chatAttachmentError = null;
  void loadAttachmentFromFile(file)
    .then((attachment) => {
      state.chatAttachments = [attachment];
      state.chatAttachmentError = null;
    })
    .catch((error: unknown) => {
      console.error("[terminal] failed to read assistant attachment:", error);
      state.chatAttachmentError = "读取文件失败，请重试";
    })
    .finally(() => {
      input.value = "";
    });
}

function insertHostContext(state: AppViewState, host: TerminalHost) {
  const draft = state.terminalAssistantDraft;
  const withoutTrigger = draft.endsWith("@") ? draft.slice(0, -1) : draft;
  const separator = withoutTrigger && !/\s$/.test(withoutTrigger) ? " " : "";
  state.terminalAssistantDraft = `${withoutTrigger}${separator}@${host.host} `;
}

const resourceItems = [
  { label: "主机", icon: "▣" },
  { label: "SFTP", icon: "▱" },
  { label: "密钥串", icon: "⚿" },
  { label: "脚本", icon: "⌘" },
  { label: "笔记", icon: "▤" },
  { label: "已知主机", icon: "◉" },
  { label: "日志", icon: "≡" },
  { label: "设置", icon: "⚙" },
];

function textFromMessage(message: unknown): string {
  if (!message || typeof message !== "object") {
    return "";
  }
  const item = message as { content?: unknown; text?: unknown };
  if (typeof item.text === "string") {
    return item.text;
  }
  if (typeof item.content === "string") {
    return item.content;
  }
  if (Array.isArray(item.content)) {
    const text = item.content
      .map((block) => {
        if (typeof block === "string") return block;
        if (!block || typeof block !== "object") return "";
        const text = (block as { text?: unknown }).text;
        return typeof text === "string" ? text : "";
      })
      .filter(Boolean)
      .join("\n");
    return text || extractA2UIDisplayText(message) || "";
  }
  return extractA2UIDisplayText(message) ?? "";
}

function roleFromMessage(message: unknown): string {
  if (!message || typeof message !== "object") return "assistant";
  const role = (message as { role?: unknown }).role;
  return role === "user" ? "user" : "assistant";
}

function formatTimestamp(message: unknown): string {
  if (!message || typeof message !== "object") return "";
  const raw = (message as { timestamp?: unknown }).timestamp;
  if (typeof raw !== "number" && typeof raw !== "string") return "";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

function resizeAssistantPanel(state: AppViewState, event: PointerEvent) {
  if (event.button !== 0) return;
  const divider = event.currentTarget;
  if (!(divider instanceof HTMLElement)) return;
  const body = divider.parentElement;
  if (!body) return;
  event.preventDefault();
  divider.setPointerCapture(event.pointerId);
  const update = (pointerEvent: PointerEvent) => {
    const bounds = body.getBoundingClientRect();
    state.terminalAssistantWidth = Math.max(
      300,
      Math.min(560, Math.round(bounds.right - pointerEvent.clientX)),
    );
  };
  const stop = () => {
    window.removeEventListener("pointermove", update);
    window.removeEventListener("pointerup", stop);
    window.removeEventListener("pointercancel", stop);
  };
  window.addEventListener("pointermove", update);
  window.addEventListener("pointerup", stop, { once: true });
  window.addEventListener("pointercancel", stop, { once: true });
}

async function copyText(state: AppViewState, value: string) {
  try {
    await navigator.clipboard.writeText(value);
    state.terminalFeedback = "已复制到剪贴板";
  } catch (error) {
    console.error("[terminal] clipboard write failed:", error);
    state.terminalFeedback = "复制失败，请检查浏览器剪贴板权限";
  }
}

function renderMessage(state: AppViewState, message: unknown, index: number) {
  const role = roleFromMessage(message);
  const text = textFromMessage(message);
  const commands = [...text.matchAll(/```(?:bash|sh|shell)?\s*\n([\s\S]*?)```/gi)]
    .map((match) => match[1]?.trim() ?? "")
    .filter(Boolean);
  const thinking = role === "assistant" && Boolean(state.chatReasoningStream || commands.length);
  if (!text && role !== "assistant") return nothing;
  return html`
    <article class="terminal-ai-message terminal-ai-message--${role}" data-message-index=${index}>
      <div class="terminal-ai-message__label">${role === "user" ? "你" : "AI"}</div>
      ${thinking
        ? html`
            <details class="terminal-ai-thinking">
              <summary>✦ 已思考 1 步</summary>
              <p>思考过程已折叠，仅展示结论。</p>
            </details>
          `
        : nothing}
      ${text
        ? html`<div class="terminal-ai-message__body">${unsafeHTML(toSanitizedMarkdownHtml(text))}</div>`
        : nothing}
      ${commands.map(
        (command) => html`
          <div class="terminal-ai-command">
            <pre><code>${command}</code></pre>
            <div class="terminal-ai-command__actions">
              <button type="button" @click=${() => void copyText(state, command)}>
                ${icons.copy}<span>复制命令</span>
              </button>
              <button
                type="button"
                title="将命令放入终端草稿；不会自动执行"
                @click=${() => {
                  state.terminalWorkspace.proposeCommand(command, state.terminalActiveHostId);
                  const target = state.terminalHosts.find(
                    (host) => host.id === state.terminalCommandTargetHostId,
                  );
                  state.terminalFeedback = target
                    ? `命令已放入 ${target.username}@${target.host} 的终端草稿，尚未执行。`
                    : "命令已放入终端草稿；请先选择目标主机，命令尚未执行。";
                }}
              >
                <span>放入终端草稿</span>
              </button>
            </div>
          </div>
        `,
      )}
      ${formatTimestamp(message)
        ? html`<time class="terminal-ai-message__time">${formatTimestamp(message)}</time>`
        : nothing}
    </article>
  `;
}

export function renderTerminalWorkspace(state: AppViewState) {
  state = terminalAssistantView(state);
  const activeHost = state.terminalHosts.find((host) => host.id === state.terminalActiveHostId) ?? null;
  const commandTargetHost =
    state.terminalCommandTargetHostId === null
      ? null
      : state.terminalHosts.find((host) => host.id === state.terminalCommandTargetHostId) ?? null;
  const renderedMessages = (state.chatMessages ?? []).map((message, index) =>
    renderMessage(state, message, index),
  );
  if (state.chatStream) {
    renderedMessages.push(html`
      <article class="terminal-ai-message terminal-ai-message--assistant">
        <div class="terminal-ai-message__label">AI</div>
        <div class="terminal-ai-message__body">${unsafeHTML(toSanitizedMarkdownHtml(state.chatStream))}</div>
      </article>
    `);
  } else if (state.chatRunId) {
    renderedMessages.push(html`
      <article class="terminal-ai-message terminal-ai-message--assistant terminal-ai-message--status" role="status" aria-live="polite">
        <div class="terminal-ai-message__label">AI</div>
        <div class="terminal-ai-message__body">
          ${state.chatRunPhase === "tool" ? "正在调用工具…" : "正在思考…"}
        </div>
      </article>
    `);
  }
  const availableModels = Object.entries(
    (
      state.configSnapshot?.config as
        | { models?: { providers?: Record<string, { models?: Array<{ id?: string; name?: string }> }> } }
        | undefined
    )?.models?.providers ?? {},
  ).flatMap(([provider, config]) =>
    (config.models ?? [])
      .filter((model) => model.id)
      .map((model) => ({
        value: `${provider}/${model.id}`,
        label: model.name ? `${model.name} (${provider}/${model.id})` : `${provider}/${model.id}`,
      })),
  );
  const skills = (state.skillsReport?.skills ?? []).filter((skill) => !skill.disabled && skill.eligible);
  const connectionStatus = activeHost?.connected ? "已连接" : "未连接 SSH";

  return html`
    <div
      class="terminal-app ${state.terminalSidebarCollapsed ? "terminal-app--sidebar-collapsed" : ""}"
      style="--terminal-ai-width: ${state.terminalAssistantWidth}px"
    >
      <header class="terminal-topbar">
        <div class="terminal-brand">
          <img src="/logo_h.png" alt="" />
          <span>OpenOcta <strong>运维智能体</strong></span>
          <small>2.2.0</small>
        </div>
        <nav class="terminal-main-nav" aria-label="主导航">
          <button
            type="button"
            class=${state.activeWorkspace === "agents" ? "is-active" : ""}
            @click=${() => {
              state.activeWorkspace = "agents";
              state.setTab("message");
            }}
          >智能体</button>
          <button
            type="button"
            class=${state.activeWorkspace === "terminal" ? "is-active" : ""}
            @click=${() => (state.activeWorkspace = "terminal")}
          >▣ 终端管理</button>
          <button
            type="button"
            class=${state.activeWorkspace === "knowledge" ? "is-active" : ""}
            @click=${() => {
              state.activeWorkspace = "knowledge";
              state.setTab("knowledgeVault");
            }}
          >知识管理</button>
        </nav>
        <div class="terminal-topbar-actions">
          <button type="button" aria-label="通知" title="通知">♧</button>
          <button
            type="button"
            aria-label="插件"
            title="工具与插件"
            @click=${() => {
              state.activeWorkspace = "agents";
              state.setTab("toolLibrary");
            }}
          >⊞</button>
          <span class="terminal-user-avatar">${(state.assistantName || "U").slice(0, 1)}</span>
          <span class="terminal-user-name">${state.assistantName || "用户"}</span>
          ${state.isWindowsDesktop
            ? html`
                <div class="terminal-window-controls">
                  <button type="button" aria-label="最小化窗口" @click=${() => state.handleWindowMinimise()}>−</button>
                  <button type="button" aria-label="最大化窗口" @click=${() => state.handleWindowToggleMaximise()}>□</button>
                  <button type="button" aria-label="关闭窗口" @click=${() => state.handleWindowClose()}>×</button>
                </div>
              `
            : nothing}
        </div>
      </header>

      <div class="terminal-workspace__body">
        <aside class="terminal-resource-sidebar">
          <div class="terminal-resource-sidebar__heading">主机</div>
          <nav class="terminal-resource-nav" aria-label="终端资源">
            ${resourceItems.map(
              (item) => html`
                <button
                  type="button"
                  title=${item.label}
                  class=${state.terminalSection === item.label ? "is-active" : ""}
                  @click=${() => (state.terminalSection = item.label)}
                >
                  <span class="terminal-resource-nav__icon" aria-hidden="true">${item.icon}</span>
                  <span>${item.label}</span>
                </button>
              `,
            )}
          </nav>
          <button
            type="button"
            class="terminal-sidebar-toggle"
            aria-label=${state.terminalSidebarCollapsed ? "展开侧边栏" : "折叠侧边栏"}
            title=${state.terminalSidebarCollapsed ? "展开侧边栏" : "折叠侧边栏"}
            @click=${() => (state.terminalSidebarCollapsed = !state.terminalSidebarCollapsed)}
          >${state.terminalSidebarCollapsed ? "›" : "‹"}</button>
        </aside>

        <main class="terminal-center-pane">
          <div class="terminal-session-tabs">
            <div class="terminal-session-tabs__title">▣ <span>${state.terminalSection}</span></div>
            ${state.terminalHosts.map(
              (host) => html`
                <div class="terminal-session-tab ${host.id === state.terminalActiveHostId ? "is-active" : ""}">
                  <span class="terminal-session-tab__status ${host.connected ? "is-connected" : ""}"></span>
                  <button
                    type="button"
                    class="terminal-session-tab__select"
                    aria-label="${host.host}，${host.connected ? "已连接" : "待连接"}"
                    @click=${() => (state.terminalActiveHostId = host.id)}
                  >
                    ${host.host}
                    <span class="terminal-session-tab__pending">${host.connected ? "已连接" : "待连接"}</span>
                  </button>
                  <button
                    type="button"
                    class="terminal-session-tab__close"
                    aria-label="移除主机标签"
                    title="移除主机标签"
                    @click=${(event: Event) => {
                      event.stopPropagation();
                        void removeTerminalHost(state, host);
                      }}
                      >×</button>
                  </div>
              `,
            )}
            <button
              class="terminal-session-tabs__add"
              type="button"
              aria-label="新建连接"
              title="添加主机"
              @click=${() => openTerminalHostDialog(state)}
            >＋</button>
          </div>
          <section class="terminal-workspace-panel">
            <div class="terminal-toolbar">
              <div class="terminal-toolbar__identity">
                <strong>${activeHost ? `${activeHost.username}@${activeHost.host}` : "尚未选择主机"}</strong>
                ${activeHost
                  ? html`<span class="terminal-status ${activeHost.connected ? "is-connected" : ""}">${connectionStatus}</span>`
                  : nothing}
                ${activeHost
                  ? html`
                      <button
                        type="button"
                        class="terminal-connection-action"
                        @click=${() => {
                          if (activeHost.connected) {
                            void disconnectTerminalHost(state, activeHost);
                          } else {
                            openTerminalHostDialog(state, activeHost);
                          }
                        }}
                      >${activeHost.connected ? "断开连接" : "连接"}</button>
                    `
                  : nothing}
              </div>
              <div class="terminal-metrics" aria-label="系统资源状态">
                <span>CPU <b>—</b></span>
                <span>MEM <b>—</b></span>
                <span>DISK <b>—</b></span>
                <span>NET <b>—</b></span>
              </div>
              <div class="terminal-toolbar__actions">
                ${state.terminalSearchOpen
                  ? html`<input
                      class="terminal-search-input"
                      type="search"
                      aria-label="搜索终端内容"
                      placeholder="搜索终端内容"
                      autofocus
                    />`
                  : nothing}
                <button
                  type="button"
                  aria-label="搜索终端内容"
                  title="搜索"
                  @click=${() => (state.terminalSearchOpen = !state.terminalSearchOpen)}
                >${icons.search}</button>
                <button type="button" aria-label="更多终端操作" title="更多操作">${icons.moreHorizontal}</button>
              </div>
            </div>
            <div class="terminal-viewport" aria-label="SSH 终端">
              ${activeHost?.connected
                ? html`<div class="terminal-xterm-root" aria-label="交互式 SSH 终端"></div>`
                : html`
                    <div class="terminal-viewport__notice">
                      <span class="terminal-viewport__glyph">›_</span>
                      <strong>${activeHost ? "主机尚未连接" : "准备连接 SSH 主机"}</strong>
                      <p>添加 SSH 主机并核对服务器指纹后，即可打开交互式终端。</p>
                      <button type="button" @click=${() => openTerminalHostDialog(state, activeHost ?? undefined)}>
                        ${activeHost ? "连接主机" : html`${icons.plus}<span>添加主机</span>`}
                      </button>
                    </div>
                  `}
              ${state.terminalSessionError
                ? html`<div class="terminal-session-error" role="alert">${state.terminalSessionError}</div>`
                : nothing}
              ${state.terminalCommandDraft
                ? html`
                    <div class="terminal-command-draft">
                      <span>
                        命令草稿（尚未执行）
                        · 目标：${commandTargetHost
                          ? `${commandTargetHost.username}@${commandTargetHost.host}`
                          : "未选择主机"}
                      </span>
                      <code>${state.terminalCommandDraft}</code>
                      <div class="terminal-command-draft__actions">
                        ${!state.terminalWorkspace.resolveCommandTarget() && activeHost?.connected
                          ? html`
                              <button
                                type="button"
                                @click=${() => {
                                  state.terminalWorkspace.proposeCommand(state.terminalCommandDraft, activeHost.id);
                                  state.terminalFeedback = `命令目标已设为 ${activeHost.username}@${activeHost.host}，请检查后执行。`;
                                }}
                              >绑定当前主机</button>
                            `
                          : nothing}
                        <button
                          type="button"
                          ?disabled=${!state.terminalWorkspace.resolveCommandTarget()}
                          @click=${() =>
                            void sendTerminalCommand(
                              state,
                              state.terminalCommandDraft,
                              state.terminalCommandTargetHostId,
                            )}
                        >发送到目标主机并执行</button>
                        <button
                          type="button"
                          aria-label="清除命令草稿"
                          @click=${() => state.terminalWorkspace.clearCommand()}
                        >×</button>
                      </div>
                    </div>
                  `
                : nothing}
            </div>
          </section>
        </main>

        <div
          class="terminal-resize-handle"
          role="separator"
          aria-label="调整终端与 AI 助手宽度"
          aria-orientation="vertical"
          @pointerdown=${(event: PointerEvent) => resizeAssistantPanel(state, event)}
        ></div>

        <aside class="terminal-ai-panel">
          <div class="terminal-ai-panel__top">
            <div class="terminal-ai-panel__tabs">
              <button type="button" class="is-active" aria-label="AI 对话">☷</button>
              <button type="button" aria-label="终端 AI 助手">▤</button>
              <button type="button" aria-label="运行状态">⌁</button>
              <button type="button" aria-label="快捷操作">ϟ</button>
            </div>
            <button type="button" aria-label="收起 AI 助手" title="收起 AI 助手">×</button>
          </div>
          <div class="terminal-ai-panel__heading">
            <strong>终端 AI 助手</strong>
            <div>
              <button
                type="button"
                aria-label="对话历史"
                title="对话历史"
                @click=${() => {
                  state.activeWorkspace = "agents";
                  state.setTab("message");
                }}
              >${icons.historyClock}</button>
              <button
                type="button"
                aria-label="新建对话"
                title="新建对话"
                @click=${() => void state.terminalAssistant.newConversation()}
              >＋</button>
              <button
                type="button"
                aria-label="导出对话"
                title="导出对话"
                @click=${() => exportConversation(state)}
              >${icons.download}</button>
              <button
                type="button"
                aria-label="清空对话"
                title="清空对话"
                @click=${() => void state.terminalAssistant.reset()}
              >${icons.trash}</button>
            </div>
          </div>
          <div class="terminal-ai-connection ${activeHost?.connected ? "is-connected" : ""}" role="status">
            <span class="terminal-ai-connection__dot"></span>
            ${activeHost?.connected
              ? `已关联 ${activeHost.username}@${activeHost.host}`
              : "未连接 SSH 主机，回答不会包含终端现场信息"}
          </div>
          <div class="terminal-ai-messages" @scroll=${(event: Event) => state.handleChatScroll(event)}>
            ${renderedMessages.length
              ? renderedMessages
              : html`
                  <article class="terminal-ai-message terminal-ai-message--assistant">
                    <div class="terminal-ai-message__label">AI</div>
                    <div class="terminal-ai-message__body">
                        你好，我是你的终端 AI 助手。输入 @ 并选择已连接主机后，我会结合最近的终端输出协助你。
                    </div>
                    <time class="terminal-ai-message__time">现在</time>
                  </article>
                `}
          </div>
          <div class="terminal-ai-composer">
            ${state.terminalAssistant.active.lastError
              ? html`<div role="alert">${state.terminalAssistant.active.lastError}</div>`
              : nothing}
            ${state.chatRunId
              ? html`<button type="button" @click=${() => void state.terminalAssistant.stop()}>停止生成</button>`
              : nothing}
            <div class="terminal-ai-composer__context-hint">
              ${activeHost?.connected
                ? `输入 @ 选择 ${activeHost.username}@${activeHost.host}，最近终端输出会随问题发送给 AI`
                : "输入 @ 或点「添加上下文」选择主机；连接 SSH 后可附带最近终端输出"}
            </div>
            ${state.chatAttachments.length
              ? html`
                  <div class="terminal-ai-attachment" role="status">
                    <span>${state.chatAttachments[0].filename || "已添加附件"}</span>
                    <button
                      type="button"
                      aria-label="移除附件"
                      @click=${() => {
                        state.chatAttachments = [];
                        state.chatAttachmentError = null;
                      }}
                    >×</button>
                  </div>
                `
              : nothing}
            ${state.chatAttachmentError
              ? html`<div class="terminal-ai-attachment-error" role="alert">${state.chatAttachmentError}</div>`
              : nothing}
            ${state.terminalContextPickerOpen
              ? html`
                  <div class="terminal-context-picker" role="listbox" aria-label="选择终端主机上下文">
                    ${state.terminalHosts.length
                      ? state.terminalHosts.map(
                          (host) => html`
                            <button
                              type="button"
                              role="option"
                              aria-selected=${String(host.id === state.terminalActiveHostId)}
                              @click=${() => {
                                state.terminalActiveHostId = host.id;
                                insertHostContext(state, host);
                                state.terminalContextPickerOpen = false;
                              }}
                            >${host.username}@${host.host} <small>${host.connected ? "已连接" : "待连接"}</small></button>
                          `,
                        )
                      : html`
                          <p>还没有主机</p>
                          <button type="button" @click=${() => {
                            state.terminalContextPickerOpen = false;
                            state.terminalHostDialogOpen = true;
                          }}>＋ 添加主机</button>
                        `}
                  </div>
                `
              : nothing}
            <textarea
              .value=${state.terminalAssistantDraft}
              placeholder=${state.connected ? "向终端 AI 助手提问…" : "网关未连接"}
              aria-label="向终端 AI 助手发送消息"
              ?disabled=${!state.connected}
              @input=${(event: Event) => {
                const input = event.currentTarget as HTMLTextAreaElement;
                state.terminalAssistantDraft = input.value;
                if (input.value.endsWith("@")) {
                  state.terminalContextPickerOpen = true;
                }
              }}
              @keydown=${(event: KeyboardEvent) => {
                if (event.key === "Escape") state.terminalContextPickerOpen = false;
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  if (state.terminalAssistantDraft.trim() && state.connected && !state.chatRunId && !state.chatSending) {
                    const draft = state.terminalAssistantDraft;
                    state.terminalAssistantDraft = "";
                    void state.handleTerminalAssistantSend(draft);
                  }
                }
              }}
            ></textarea>
            <div class="terminal-ai-composer__toolbar">
              <input
                class="terminal-ai-attachment-input"
                type="file"
                aria-label="添加附件"
                hidden
                @change=${(event: Event) => handleTerminalAttachmentPick(state, event)}
              />
              <button
                type="button"
                aria-label="添加附件"
                title="添加图片或文档"
                ?disabled=${!state.connected}
                @click=${(event: Event) => {
                  const toolbar = (event.currentTarget as HTMLElement).parentElement;
                  toolbar?.querySelector<HTMLInputElement>(".terminal-ai-attachment-input")?.click();
                }}
              >＋</button>
              <button
                type="button"
                class="terminal-composer-add-context"
                @click=${() => (state.terminalContextPickerOpen = !state.terminalContextPickerOpen)}
              >＋ <span>添加上下文</span></button>
              <select
                aria-label="选择模型"
                .value=${state.chatModelRef ?? ""}
                @change=${(event: Event) => {
                  state.chatModelRef = (event.currentTarget as HTMLSelectElement).value || null;
                }}
              >
                <option value="">⚡ Auto</option>
                ${availableModels.map((model) => html`<option value=${model.value}>${model.label}</option>`)}
              </select>
              <select aria-label="选择角色" disabled>
                <option>♙ Agent</option>
              </select>
              <select
                aria-label="选择技能"
                .value=${state.chatResources.configured ? state.chatResources.skillKeys[0] ?? "" : ""}
                @change=${(event: Event) => {
                  const skillKey = (event.currentTarget as HTMLSelectElement).value;
                  state.chatResources = {
                    ...state.chatResources,
                    configured:
                      Boolean(skillKey) ||
                      state.chatResources.mcpServers.length > 0 ||
                      !state.chatResources.webSearch,
                    skillKeys: skillKey ? [skillKey] : [],
                  };
                }}
              >
                <option value="">技能（全部）</option>
                ${skills.map((skill) => html`<option value=${skill.skillKey}>${skill.name}</option>`)}
              </select>
              <button
                type="button"
                class="terminal-ai-send"
                aria-label="发送消息"
                title=${state.connected ? "发送消息" : "请先连接网关"}
                ?disabled=${!state.connected || !state.terminalAssistantDraft.trim() || Boolean(state.chatRunId) || state.chatSending}
                @click=${() => {
                  const draft = state.terminalAssistantDraft;
                  if (!draft.trim()) return;
                  state.terminalAssistantDraft = "";
                  void state.handleTerminalAssistantSend(draft);
                }}
              >↑</button>
            </div>
          </div>
        </aside>
      </div>
      ${state.terminalFeedback
        ? html`
            <div class="terminal-feedback" role="status">
              <span>${state.terminalFeedback}</span>
              <button type="button" aria-label="关闭提示" @click=${() => (state.terminalFeedback = null)}>×</button>
            </div>
          `
        : nothing}
      ${state.terminalHostDialogOpen
        ? html`
            <div class="terminal-modal-backdrop" @click=${() => closeTerminalHostDialog(state)}>
              <section
                class="terminal-host-dialog"
                role="dialog"
                aria-modal="true"
                aria-labelledby="terminal-host-dialog-title"
                @click=${(event: Event) => event.stopPropagation()}
              >
                <div class="terminal-host-dialog__header">
                  <div>
                    <h2 id="terminal-host-dialog-title">连接 SSH 主机</h2>
                    <p>密码和私钥仅用于本次连接，不会保存到主机列表。</p>
                  </div>
                  <button type="button" aria-label="关闭" @click=${() => closeTerminalHostDialog(state)}>×</button>
                </div>
                <form
                  @submit=${(event: SubmitEvent) => {
                    event.preventDefault();
                    void connectTerminalHost(state);
                  }}
                >
                  <label for="terminal-host-address">主机地址</label>
                  <input
                    id="terminal-host-address"
                    required
                    autocomplete="off"
                    placeholder="例如 192.168.1.10"
                    .value=${state.terminalHostInput}
                    @input=${(event: Event) => (state.terminalHostInput = (event.currentTarget as HTMLInputElement).value)}
                  />
                  <label for="terminal-host-port">SSH 端口</label>
                  <input
                    id="terminal-host-port"
                    type="number"
                    min="1"
                    max="65535"
                    required
                    .value=${state.terminalPortInput}
                    @input=${(event: Event) => (state.terminalPortInput = (event.currentTarget as HTMLInputElement).value)}
                  />
                  <label for="terminal-host-user">用户名</label>
                  <input
                    id="terminal-host-user"
                    autocomplete="username"
                    .value=${state.terminalUserInput}
                    @input=${(event: Event) => (state.terminalUserInput = (event.currentTarget as HTMLInputElement).value)}
                  />
                  ${state.terminalHostFingerprint && !state.terminalHostKeyConfirmed
                    ? html`
                        <div class="terminal-host-dialog__fingerprint">
                          <strong>请核对服务器主机密钥</strong>
                          <code>${state.terminalHostFingerprint}</code>
                          <p>确认该指纹来自可信服务器后再继续；指纹不匹配时会拒绝连接。</p>
                          <button
                            type="button"
                            @click=${() => {
                              state.terminalHostKeyConfirmed = true;
                              state.terminalError = null;
                            }}
                          >信任此指纹并继续</button>
                        </div>
                      `
                    : nothing}
                  ${state.terminalHostKeyConfirmed
                    ? html`
                        <label for="terminal-ssh-password">SSH 密码</label>
                        <input
                          id="terminal-ssh-password"
                          type="password"
                          autocomplete="new-password"
                          .value=${state.terminalPasswordInput}
                          @input=${(event: Event) => (state.terminalPasswordInput = (event.currentTarget as HTMLInputElement).value)}
                        />
                        <label for="terminal-ssh-private-key">或 SSH 私钥</label>
                        <textarea
                          id="terminal-ssh-private-key"
                          rows="4"
                          autocomplete="off"
                          spellcheck="false"
                          placeholder="粘贴 OpenSSH 私钥（可选）"
                          .value=${state.terminalPrivateKeyInput}
                          @input=${(event: Event) => (state.terminalPrivateKeyInput = (event.currentTarget as HTMLTextAreaElement).value)}
                        ></textarea>
                        ${state.terminalPrivateKeyInput
                          ? html`
                              <label for="terminal-ssh-passphrase">私钥口令（如有）</label>
                              <input
                                id="terminal-ssh-passphrase"
                                type="password"
                                autocomplete="new-password"
                                .value=${state.terminalPassphraseInput}
                                @input=${(event: Event) => (state.terminalPassphraseInput = (event.currentTarget as HTMLInputElement).value)}
                              />
                            `
                          : nothing}
                      `
                    : nothing}
                  ${state.terminalError
                    ? html`<div class="terminal-host-dialog__error" role="alert">${state.terminalError}</div>`
                    : nothing}
                  <div class="terminal-host-dialog__notice">
                    首次连接先读取服务器指纹，不发送认证信息；确认指纹后才会发送密码或私钥。
                  </div>
                  ${window.location.protocol !== "https:"
                    ? html`
                        <div class="terminal-host-dialog__transport-warning" role="note">
                          当前页面使用 HTTP，浏览器到 Gateway 之间没有 TLS 加密；请仅在可信网络中输入 SSH 凭据。
                        </div>
                      `
                    : nothing}
                  <div class="terminal-host-dialog__actions">
                    <button type="button" @click=${() => closeTerminalHostDialog(state)}>取消</button>
                    <button class="is-primary" type="submit" ?disabled=${state.terminalConnecting}>
                      ${state.terminalConnecting
                        ? "正在连接…"
                        : state.terminalHostKeyConfirmed
                          ? "连接 SSH"
                          : "获取服务器指纹"}
                    </button>
                  </div>
                </form>
              </section>
            </div>
          `
        : nothing}
    </div>
  `;
}
