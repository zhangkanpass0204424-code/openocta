import { html, nothing } from "lit";
import { ref } from "lit/directives/ref.js";
import type { SessionsListResult } from "../types.ts";
import type { ChatSessionResources } from "../chat/chat-resources.ts";
import { chatResourcesSelectionCount } from "../chat/chat-resources.ts";
import type { SkillStatusEntry } from "../types.ts";
import type { ChatAttachment, ChatQueueItem } from "../ui-types.ts";
import type { GatewayBrowserClient } from "../gateway.ts";
import type { FilePreviewRequest } from "../chat/file-blocks.ts";
import { icons } from "../icons.ts";
import { nativeConfirm } from "../native-dialog-bridge.ts";
import { t } from "../strings.js";
import { DEFAULT_CHAT_QUICK_PROMPTS } from "../scenario-templates.ts";
import "../chat-suggestion-prompts.ts";
import "../components/resizable-divider.ts";
import "../components/chat-file-preview.ts";
import { renderMarkdownSidebar } from "./markdown-sidebar.ts";
import "../components/openocta-chat-thread.ts";
import "../components/openocta-chat-input.ts";
import "../components/openocta-local-agent-picker.ts";
import type { LocalAgentProbeResult } from "../local-agents.ts";

export type CompactionIndicatorStatus = {
  active: boolean;
  startedAt: number | null;
  completedAt: number | null;
};

export type ChatProps = {
  sessionKey: string;
  onSessionKeyChange: (next: string) => void;
  thinkingLevel: string | null;
  showThinking: boolean;
  modelRef?: string | null;
  defaultModelRef?: string | null;
  modelOptions?: Array<{ value: string; label: string }>;
  onModelRefChange?: (next: string | null) => void;
  resources?: ChatSessionResources;
  resourcesPanelOpen?: boolean;
  resourcesTab?: "skills" | "mcp";
  resourcesSkillSearch?: string;
  resourcesMcpSearch?: string;
  onResourcesPanelToggle?: () => void;
  onResourcesPanelClose?: () => void;
  onResourcesTabChange?: (tab: "skills" | "mcp") => void;
  onResourcesSkillSearchChange?: (query: string) => void;
  onResourcesMcpSearchChange?: (query: string) => void;
  onResourcesChange?: (next: ChatSessionResources) => void;
  resourceSkillOptions?: SkillStatusEntry[];
  resourceMcpOptions?: Array<{ key: string; label: string }>;
  canExtractSkill?: boolean;
  extractSkillLoading?: boolean;
  extractSkillError?: string | null;
  extractSkillOpen?: boolean;
  extractSkillMarkdown?: string | null;
  extractSkillFilename?: string | null;
  onExtractSkill?: () => void;
  onCloseExtractSkill?: () => void;
  onDownloadExtractSkill?: () => void;
  loading: boolean;
  sending: boolean;
  canAbort?: boolean;
  compactionStatus?: CompactionIndicatorStatus | null;
  messages: unknown[];
  toolMessages: unknown[];
  stream: string | null;
  reasoningStream?: string | null;
  streamStartedAt: number | null;
  runPhase?: "idle" | "thinking" | "tool" | "streaming";
  a2uiMessages?: unknown[];
  client?: GatewayBrowserClient | null;
  onA2UIAction?: (action: import("@a2ui/web_core/v0_9").A2uiClientAction) => Promise<void> | void;
  assistantAvatarUrl?: string | null;
  draft: string;
  /** True when the isolated compose input has non-whitespace text. */
  composeHasText?: boolean;
  composeClearToken?: number;
  queue: ChatQueueItem[];
  connected: boolean;
  canSend: boolean;
  disabledReason: string | null;
  error: string | null;
  sessions: SessionsListResult | null;
  // Focus mode
  focusMode: boolean;
  // Sidebar state
  sidebarOpen?: boolean;
  sidebarContent?: string | null;
  sidebarError?: string | null;
  splitRatio?: number;
  assistantName: string;
  assistantAvatar: string | null;
  // Attachments (single file: image or common document)
  attachments?: ChatAttachment[];
  attachmentError?: string | null;
  onAttachmentsChange?: (attachments: ChatAttachment[]) => void;
  onAttachmentError?: (message: string | null) => void;
  // Scroll control
  showNewMessages?: boolean;
  onScrollToBottom?: () => void;
  /** When true, thread shows only assistant/user (no tool rows). When false, tool calls appear with I/O folded. */
  conversationOnly?: boolean;
  onConversationOnlyChange?: (next: boolean) => void;
  /** 空会话快捷输入；未传时使用内置默认文案 */
  quickPrompts?: string[];
  localAgents?: LocalAgentProbeResult[];
  composeInsertToken?: number;
  composeInsertSnippet?: string;
  onComposeInsert?: (snippet: string) => void;
  // Event handlers
  onRefresh: () => void;
  onToggleFocusMode: () => void;
  onDraftChange: (next: string) => void;
  onComposeDraftChange?: (hasText: boolean) => void;
  onSend: (message?: string) => void;
  onAbort?: () => void;
  onQueueRemove: (id: string) => void;
  confirmQueueRemove?: boolean;
  onNewSession: () => void;
  onOpenSidebar?: (content: string) => void;
  onCloseSidebar?: () => void;
  onSplitRatioChange?: (ratio: number) => void;
  onChatScroll?: (event: Event) => void;
  filePreview?: FilePreviewRequest | null;
  onFilePreview?: (req: FilePreviewRequest) => void;
  onCloseFilePreview?: () => void;
  onOpenAttachment?: (path: string) => void;
};

const COMPACTION_TOAST_DURATION_MS = 5000;
/** 图片与文档上限 5MB */
export const CHAT_ATTACHMENT_MAX_BYTES = 5 * 1024 * 1024;
/** 视频上限 50MB */
export const CHAT_ATTACHMENT_VIDEO_MAX_BYTES = 50 * 1024 * 1024;
const CHAT_ATTACHMENT_MAX_COUNT = 1;

const CHAT_ATTACHMENT_BLOCKED_EXTENSIONS = new Set([
  ".zip",
  ".rar",
  ".7z",
  ".tar",
  ".gz",
  ".gzip",
  ".bz2",
  ".bzip2",
  ".xz",
  ".tgz",
  ".tbz",
  ".tbz2",
  ".tar.gz",
  ".tar.bz2",
  ".tar.xz",
  ".iso",
  ".dmg",
  ".apk",
  ".deb",
  ".rpm",
  ".exe",
  ".msi",
  ".bin",
  ".jar",
  ".war",
  ".cab",
  ".lz",
  ".lzma",
  ".zst",
]);

const CHAT_ATTACHMENT_ALLOWED_EXTENSIONS = new Set([
  ".png",
  ".jpg",
  ".jpeg",
  ".gif",
  ".webp",
  ".bmp",
  ".svg",
  ".pdf",
  ".txt",
  ".md",
  ".markdown",
  ".csv",
  ".json",
  ".xml",
  ".html",
  ".htm",
  ".doc",
  ".docx",
  ".xls",
  ".xlsx",
  ".ppt",
  ".pptx",
  ".rtf",
  ".mp4",
  ".mov",
  ".avi",
]);

const CHAT_ATTACHMENT_ACCEPT = [
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/bmp",
  "image/svg+xml",
  "application/pdf",
  "text/plain",
  "text/markdown",
  "text/csv",
  "application/json",
  "application/xml",
  "text/xml",
  "text/html",
  "application/msword",
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  "application/vnd.ms-excel",
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  "application/vnd.ms-powerpoint",
  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  "application/rtf",
  "video/mp4",
  "video/quicktime",
  "video/x-msvideo",
  ".png,.jpg,.jpeg,.gif,.webp,.bmp,.svg,.pdf,.txt,.md,.csv,.json,.xml,.html,.htm,.doc,.docx,.xls,.xlsx,.ppt,.pptx,.rtf,.mp4,.mov,.avi",
].join(",");

type AttachmentValidationResult = { ok: true } | { ok: false; message: string };

function getFileExtension(filename: string): string {
  const lower = filename.trim().toLowerCase();
  for (const ext of CHAT_ATTACHMENT_BLOCKED_EXTENSIONS) {
    if (lower.endsWith(ext)) {
      return ext;
    }
  }
  const dot = lower.lastIndexOf(".");
  return dot >= 0 ? lower.slice(dot) : "";
}

function isBlockedAttachmentFilename(filename: string): boolean {
  const lower = filename.trim().toLowerCase();
  for (const ext of CHAT_ATTACHMENT_BLOCKED_EXTENSIONS) {
    if (lower.endsWith(ext)) {
      return true;
    }
  }
  return false;
}

function isBlockedAttachmentMime(mimeType: string): boolean {
  const lower = mimeType.trim().toLowerCase();
  if (!lower) {
    return false;
  }
  return (
    lower.includes("zip") ||
    lower.includes("x-rar") ||
    lower.includes("x-7z") ||
    lower.includes("x-tar") ||
    lower.includes("gzip") ||
    lower.includes("x-bzip")
  );
}

function isVideoAttachmentFilename(filename: string): boolean {
  const ext = getFileExtension(filename);
  return ext === ".mp4" || ext === ".mov" || ext === ".avi";
}

function isVideoAttachmentMime(mimeType: string): boolean {
  return mimeType.trim().toLowerCase().startsWith("video/");
}

function isVideoAttachmentFile(file: Pick<File, "name" | "type">): boolean {
  return isVideoAttachmentMime(file.type) || isVideoAttachmentFilename(file.name);
}

function getChatAttachmentMaxBytes(file: Pick<File, "name" | "type">): number {
  return isVideoAttachmentFile(file) ? CHAT_ATTACHMENT_VIDEO_MAX_BYTES : CHAT_ATTACHMENT_MAX_BYTES;
}

export function validateChatAttachmentFile(file: File): AttachmentValidationResult {
  const maxBytes = getChatAttachmentMaxBytes(file);
  if (file.size > maxBytes) {
    return {
      ok: false,
      message: `文件大小不能超过 ${formatBytes(maxBytes)}（当前 ${formatBytes(file.size)}）`,
    };
  }
  if (isBlockedAttachmentFilename(file.name) || isBlockedAttachmentMime(file.type)) {
    return { ok: false, message: "不支持压缩包或可执行文件" };
  }
  const ext = getFileExtension(file.name);
  if (!ext || !CHAT_ATTACHMENT_ALLOWED_EXTENSIONS.has(ext)) {
    return {
      ok: false,
      message: "仅支持常见图片、文档与视频格式（如 PNG、PDF、MP4、MOV 等）",
    };
  }
  return { ok: true };
}

function formatDurationShort(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)}s`;
  const mins = Math.floor(ms / 60000);
  const secs = Math.round((ms % 60000) / 1000);
  return `${mins}m${secs.toString().padStart(2, "0")}s`;
}

function formatBytes(bytes?: number) {
  if (bytes == null || !Number.isFinite(bytes)) {
    return "-";
  }
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  const units = ["KB", "MB", "GB", "TB"];
  let size = bytes / 1024;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size < 10 ? 1 : 0)} ${units[unitIndex]}`;
}

function renderCompactionIndicator(status: CompactionIndicatorStatus | null | undefined) {
  if (!status) {
    return nothing;
  }

  // Show "compacting..." while active
  if (status.active) {
    return html`
      <div class="callout info compaction-indicator compaction-indicator--active">
        ${icons.loader} Compacting context...
      </div>
    `;
  }

  // Show "compaction complete" briefly after completion
  if (status.completedAt) {
    const elapsed = Date.now() - status.completedAt;
    if (elapsed < COMPACTION_TOAST_DURATION_MS) {
      return html`
        <div class="callout success compaction-indicator compaction-indicator--complete">
          ${icons.check} Context compacted
        </div>
      `;
    }
  }

  return nothing;
}

function generateAttachmentId(): string {
  return `att-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

function inferAttachmentKind(mimeType: string, filename?: string): "image" | "video" | "file" {
  if (mimeType.startsWith("image/")) {
    return "image";
  }
  if (mimeType.startsWith("video/")) {
    return "video";
  }
  const ext = filename ? getFileExtension(filename) : "";
  if ([".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg"].includes(ext)) {
    return "image";
  }
  if ([".mp4", ".mov", ".avi"].includes(ext)) {
    return "video";
  }
  return "file";
}

export function loadAttachmentFromFile(file: File): Promise<ChatAttachment> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => {
      const dataUrl = reader.result as string;
      resolve({
        id: generateAttachmentId(),
        dataUrl,
        mimeType: file.type || "application/octet-stream",
        filename: file.name,
        sizeBytes: file.size,
        kind: inferAttachmentKind(file.type || "", file.name),
      });
    });
    reader.addEventListener("error", () => {
      reject(new Error("读取文件失败"));
    });
    reader.readAsDataURL(file);
  });
}

function handleFilePick(e: Event, props: ChatProps) {
  const input = e.target as HTMLInputElement | null;
  const files = input?.files ? Array.from(input.files) : [];
  if (!files.length || !props.onAttachmentsChange) {
    return;
  }
  if (files.length > CHAT_ATTACHMENT_MAX_COUNT) {
    props.onAttachmentError?.("每次只能上传 1 个文件");
    if (input) {
      input.value = "";
    }
    return;
  }

  const file = files[0];
  const validation = validateChatAttachmentFile(file);
  if (!validation.ok) {
    props.onAttachmentError?.(validation.message);
    if (input) {
      input.value = "";
    }
    return;
  }

  props.onAttachmentError?.(null);
  void loadAttachmentFromFile(file)
    .then((attachment) => {
      props.onAttachmentsChange?.([attachment]);
    })
    .catch(() => {
      props.onAttachmentError?.("读取文件失败，请重试");
    });

  if (input) {
    input.value = "";
  }
}

function handlePaste(e: ClipboardEvent, props: ChatProps) {
  const items = e.clipboardData?.items;
  if (!items || !props.onAttachmentsChange) {
    return;
  }

  let pastedFile: File | null = null;
  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    if (!item.type.startsWith("image/")) {
      continue;
    }
    const file = item.getAsFile();
    if (file) {
      pastedFile = file;
      break;
    }
  }

  if (!pastedFile) {
    return;
  }

  const validation = validateChatAttachmentFile(pastedFile);
  if (!validation.ok) {
    e.preventDefault();
    props.onAttachmentError?.(validation.message);
    return;
  }

  e.preventDefault();
  props.onAttachmentError?.(null);
  void loadAttachmentFromFile(pastedFile)
    .then((attachment) => {
      props.onAttachmentsChange?.([attachment]);
    })
    .catch(() => {
      props.onAttachmentError?.("读取粘贴内容失败，请重试");
    });
}

const SKILL_SOURCE_LABELS: Record<string, string> = {
  "openclaw-workspace": "工作区",
  "openclaw-managed": "托管",
  "openclaw-bundled": "内置",
  "openclaw-extra": "扩展",
  "employee-managed": "员工",
};

function skillSourceLabel(source: string): string {
  const key = source.trim();
  return SKILL_SOURCE_LABELS[key] ?? (key || "其他");
}

function matchesResourceSearch(query: string, ...parts: Array<string | undefined>): boolean {
  const q = query.trim().toLowerCase();
  if (!q) {
    return true;
  }
  return parts.some((part) => (part ?? "").toLowerCase().includes(q));
}

function groupSkillsBySource(skills: SkillStatusEntry[]): Array<{ source: string; label: string; items: SkillStatusEntry[] }> {
  const groups = new Map<string, SkillStatusEntry[]>();
  const order: string[] = [];
  for (const skill of skills) {
    const source = skill.source?.trim() || "other";
    if (!groups.has(source)) {
      groups.set(source, []);
      order.push(source);
    }
    groups.get(source)!.push(skill);
  }
  const priority = ["openclaw-workspace", "openclaw-managed", "openclaw-bundled", "openclaw-extra", "employee-managed"];
  order.sort((a, b) => {
    const ai = priority.indexOf(a);
    const bi = priority.indexOf(b);
    if (ai === -1 && bi === -1) {
      return a.localeCompare(b);
    }
    if (ai === -1) {
      return 1;
    }
    if (bi === -1) {
      return -1;
    }
    return ai - bi;
  });
  return order.map((source) => ({
    source,
    label: skillSourceLabel(source),
    items: groups.get(source) ?? [],
  }));
}

function toggleResourceList(
  list: string[],
  key: string,
  checked: boolean,
): string[] {
  const trimmed = key.trim();
  if (!trimmed) {
    return list;
  }
  if (checked) {
    if (list.includes(trimmed)) {
      return list;
    }
    return [...list, trimmed];
  }
  return list.filter((item) => item !== trimmed);
}

function resourcesAnchorRef(props: ChatProps) {
  return (el: Element | undefined) => {
    if (!(el instanceof HTMLElement)) {
      return;
    }
    const section = el.closest(".chat");
    if (!(section instanceof HTMLElement)) {
      return;
    }
    if (!props.resourcesPanelOpen) {
      section.style.removeProperty("--chat-resources-popover-left");
      section.style.removeProperty("--chat-resources-popover-bottom");
      return;
    }
    const rect = el.getBoundingClientRect();
    section.style.setProperty("--chat-resources-popover-left", `${Math.max(8, rect.left)}px`);
    section.style.setProperty(
      "--chat-resources-popover-bottom",
      `${Math.max(8, window.innerHeight - rect.top + 8)}px`,
    );
  };
}

function stopPopoverEvent(e: Event) {
  e.stopPropagation();
}

function renderChatResourcesPopover(props: ChatProps, opts?: { fixed?: boolean }) {
  if (!props.resourcesPanelOpen || !props.resources || !props.onResourcesChange) {
    return nothing;
  }
  const resources = props.resources;
  const tab = props.resourcesTab ?? "skills";
  const skillSearch = props.resourcesSkillSearch ?? "";
  const mcpSearch = props.resourcesMcpSearch ?? "";
  const skills = (props.resourceSkillOptions ?? []).filter((skill) =>
    matchesResourceSearch(skillSearch, skill.name, skill.skillKey, skill.description),
  );
  const mcps = (props.resourceMcpOptions ?? []).filter((mcp) =>
    matchesResourceSearch(mcpSearch, mcp.key, mcp.label),
  );
  const skillGroups = groupSkillsBySource(skills);
  const selectedCount = chatResourcesSelectionCount(resources);

  const patch = (patch: Partial<ChatSessionResources>) => {
    props.onResourcesChange?.({
      ...resources,
      configured: true,
      ...patch,
    });
  };

  return html`
    <div
      class="chat-resources-popover ${opts?.fixed ? "chat-resources-popover--fixed" : ""}"
      role="dialog"
      aria-label="全部资源"
      @mousedown=${stopPopoverEvent}
      @click=${stopPopoverEvent}
    >
      <p class="chat-resources-popover__hint muted">
        不选择 Skill/MCP 时默认全部可用；连网搜索需单独勾选开启。
      </p>
      <div class="chat-resources-popover__header">
        <span>全部资源</span>
        <span class="chat-resources-popover__count muted">${
          resources.configured
            ? selectedCount > 0
              ? `已限定 ${selectedCount} 项`
              : "已限定（未勾选具体项）"
            : "默认全部可用"
        }</span>
        <button
          type="button"
          class="btn btn--icon chat-resources-popover__close"
          aria-label="关闭"
          @click=${() => props.onResourcesPanelClose?.()}
        >
          ${icons.x}
        </button>
      </div>
      <div class="chat-resources-popover__tabs" role="tablist">
        <button
          type="button"
          role="tab"
          class="chat-resources-popover__tab ${tab === "skills" ? "chat-resources-popover__tab--active" : ""}"
          aria-selected=${tab === "skills"}
          @click=${() => props.onResourcesTabChange?.("skills")}
        >
          Skills
        </button>
        <button
          type="button"
          role="tab"
          class="chat-resources-popover__tab ${tab === "mcp" ? "chat-resources-popover__tab--active" : ""}"
          aria-selected=${tab === "mcp"}
          @click=${() => props.onResourcesTabChange?.("mcp")}
        >
          MCP
        </button>
      </div>
      <label class="chat-resources-popover__search">
        <span class="chat-resources-popover__search-icon" aria-hidden="true">${icons.search}</span>
        <input
          type="search"
          placeholder=${tab === "skills" ? "搜索 Skill 名称或描述" : "搜索 MCP 服务"}
          .value=${tab === "skills" ? skillSearch : mcpSearch}
          @input=${(e: Event) => {
            const value = (e.target as HTMLInputElement).value;
            if (tab === "skills") {
              props.onResourcesSkillSearchChange?.(value);
            } else {
              props.onResourcesMcpSearchChange?.(value);
            }
          }}
        />
      </label>
      <div class="chat-resources-popover__body">
        ${
          tab === "skills"
            ? skillGroups.length
              ? skillGroups.map(
                  (group) => html`
                    <div class="chat-resources-popover__group">
                      <div class="chat-resources-popover__group-title">${group.label}</div>
                      <div class="chat-resources-popover__list">
                        ${group.items.map(
                          (skill) => html`
                            <label class="chat-resources-popover__item">
                              <input
                                type="checkbox"
                                .checked=${resources.skillKeys.includes(skill.skillKey)}
                                @change=${(e: Event) => {
                                  patch({
                                    skillKeys: toggleResourceList(
                                      resources.skillKeys,
                                      skill.skillKey,
                                      (e.target as HTMLInputElement).checked,
                                    ),
                                  });
                                }}
                              />
                              <span class="chat-resources-popover__item-text">
                                <span class="chat-resources-popover__item-name">${skill.name}</span>
                                ${
                                  skill.description
                                    ? html`<span class="chat-resources-popover__item-desc muted">${skill.description}</span>`
                                    : nothing
                                }
                              </span>
                            </label>
                          `,
                        )}
                      </div>
                    </div>
                  `,
                )
              : html`<div class="chat-resources-popover__empty muted">${
                  skillSearch.trim() ? "没有匹配的 Skill" : "暂无已启用的 Skill"
                }</div>`
            : mcps.length
              ? html`<div class="chat-resources-popover__list">
                  ${mcps.map(
                    (mcp) => html`
                      <label class="chat-resources-popover__item">
                        <input
                          type="checkbox"
                          .checked=${resources.mcpServers.includes(mcp.key)}
                          @change=${(e: Event) => {
                            patch({
                              mcpServers: toggleResourceList(
                                resources.mcpServers,
                                mcp.key,
                                (e.target as HTMLInputElement).checked,
                              ),
                            });
                          }}
                        />
                        <span class="chat-resources-popover__item-text">
                          <span class="chat-resources-popover__item-name">${mcp.label}</span>
                        </span>
                      </label>
                    `,
                  )}
                </div>`
              : html`<div class="chat-resources-popover__empty muted">${
                  mcpSearch.trim() ? "没有匹配的 MCP" : "暂无已启用的 MCP 服务"
                }</div>`
        }
      </div>
      ${
        resources.configured
          ? html`<button
              type="button"
              class="btn btn--ghost chat-resources-popover__reset"
              @click=${() =>
                props.onResourcesChange?.({
                  configured: false,
                  skillKeys: [],
                  mcpServers: [],
                  webSearch: true,
                })}
            >
              恢复默认（全部可用）
            </button>`
          : nothing
      }
    </div>
  `;
}

function renderExtractSkillModal(props: ChatProps) {
  if (!props.extractSkillOpen || !props.extractSkillMarkdown) {
    return nothing;
  }
  return html`
    <div class="chat-extract-modal" @click=${(e: Event) => e.stopPropagation()}>
      <div class="chat-extract-modal__backdrop" @click=${() => props.onCloseExtractSkill?.()}></div>
      <div class="chat-extract-modal__card" role="dialog" aria-label="提炼 Skill">
        <div class="chat-extract-modal__header">
          <span>提炼 Skill</span>
          <button type="button" class="btn btn--icon" aria-label="关闭" @click=${() => props.onCloseExtractSkill?.()}>
            ${icons.x}
          </button>
        </div>
        <pre class="chat-extract-modal__preview">${props.extractSkillMarkdown}</pre>
        <div class="chat-extract-modal__actions">
          <button type="button" class="btn" @click=${() => props.onDownloadExtractSkill?.()}>
            ${icons.download} 下载 ${props.extractSkillFilename ?? "extracted-skill.md"}
          </button>
          <button type="button" class="btn primary" @click=${() => props.onCloseExtractSkill?.()}>关闭</button>
        </div>
      </div>
    </div>
  `;
}

function renderAttachmentPreview(props: ChatProps) {
  const attachments = props.attachments ?? [];
  if (attachments.length === 0) {
    return nothing;
  }

  return html`
    <div class="chat-attachments">
      ${attachments.map(
        (att) => html`
          <div class="chat-attachment">
            ${
              (() => {
                const kind = att.kind ?? inferAttachmentKind(att.mimeType, att.filename);
                if (kind === "image") {
                  return html`
                    <img
                      src=${att.dataUrl}
                      alt=${att.filename || "Attachment preview"}
                      class="chat-attachment__img"
                    />
                  `;
                }
                if (kind === "video") {
                  return html`
                    <video
                      src=${att.dataUrl}
                      class="chat-attachment__video"
                      controls
                      preload="metadata"
                      aria-label=${att.filename || "Video preview"}
                    ></video>
                  `;
                }
                return html`
                  <div class="chat-attachment__file">
                    <div class="mono">${att.filename || "file"}</div>
                    <div class="muted" style="font-size: 12px;">
                      ${att.mimeType}${att.sizeBytes ? ` · ${formatBytes(att.sizeBytes)}` : ""}
                    </div>
                  </div>
                `;
              })()
            }
            <button
              class="chat-attachment__remove"
              type="button"
              aria-label="Remove attachment"
              @click=${() => {
                const next = (props.attachments ?? []).filter((a) => a.id !== att.id);
                props.onAttachmentsChange?.(next);
              }}
            >
              ${icons.x}
            </button>
          </div>
        `,
      )}
    </div>
  `;
}

/** True while a chat run is in flight (abortable or send RPC pending). */
export function isChatRunActive(props: Pick<ChatProps, "canAbort" | "sending">): boolean {
  return Boolean(props.canAbort) || props.sending;
}

export function renderChat(props: ChatProps) {
  const extractingSkill = Boolean(props.extractSkillLoading);
  const canCompose = props.connected && !extractingSkill;
  const runActive = isChatRunActive(props);
  const isBusy = runActive || extractingSkill;
  const canAbort = Boolean(props.canAbort && props.onAbort);
  const activeSession = props.sessions?.sessions?.find((row) => row.key === props.sessionKey);
  const reasoningLevel = activeSession?.reasoningLevel ?? "off";
  const showReasoning = props.showThinking && reasoningLevel !== "off";
  const conversationOnly = props.conversationOnly ?? false;
  const showToolTrace = !conversationOnly;
  const assistantIdentity = {
    name: props.assistantName,
    avatar: props.assistantAvatar ?? props.assistantAvatarUrl ?? null,
  };

  const hasAttachments = (props.attachments?.length ?? 0) > 0;
  const hasDraftContent = Boolean(props.composeHasText) || props.draft.trim().length > 0;
  const canSubmit = canCompose && (hasDraftContent || hasAttachments);
  const composePlaceholder = !props.connected
    ? "Connect to the gateway to start chatting…"
    : extractingSkill
      ? "正在提炼 Skill，请稍候…"
      : hasAttachments
        ? "添加消息（也可替换附件）…"
        : "输入消息（回车发送，Shift+回车换行，可粘贴图片或添加文件，文档≤5MB，视频≤50MB）";

  const markdownSidebarOpen = Boolean(props.sidebarOpen && props.onCloseSidebar);
  const splitSidebarOpen = markdownSidebarOpen;
  const isEmptyThread =
    !props.loading &&
    (Array.isArray(props.messages) ? props.messages.length === 0 : true) &&
    !props.stream;

  const quickPrompts =
    props.quickPrompts && props.quickPrompts.length > 0
      ? props.quickPrompts
      : [...DEFAULT_CHAT_QUICK_PROMPTS];
  const emptyIntro = isEmptyThread
    ? html`
        <div class="chat-empty__title">您好，有什么可以帮助您？</div>
      `
    : nothing;
  const emptyPrompts = isEmptyThread
    ? html`
        <openocta-chat-suggestions
          .extraPrompts=${quickPrompts}
          ?disabled=${!props.connected}
          @suggestion-select=${(e: CustomEvent<{ prompt: string }>) => {
            const prompt = e.detail.prompt;
            if (props.onComposeInsert) {
              props.onComposeInsert(prompt);
              return;
            }
            props.onSend(prompt);
          }}
        ></openocta-chat-suggestions>
        ${
          (props.localAgents?.length ?? 0) > 0
            ? html`
                <div class="chat-local-agents-empty">
                  <div class="chat-local-agents-empty__hint muted">${t("chatLocalAgentsHint")}</div>
                  <openocta-local-agent-picker
                    .agents=${props.localAgents ?? []}
                    ?disabled=${!props.connected}
                    @agent-insert=${(e: CustomEvent<{ mention: string }>) =>
                      props.onComposeInsert?.(e.detail.mention)}
                  ></openocta-local-agent-picker>
                </div>
              `
            : nothing
        }
      `
    : nothing;
  const thread = html`
    <openocta-chat-thread
      .sessionKey=${props.sessionKey}
      .loading=${props.loading}
      .canAbort=${Boolean(props.canAbort)}
      .conversationOnly=${conversationOnly}
      .showReasoning=${showReasoning}
      .assistantName=${assistantIdentity.name}
      .assistantAvatar=${assistantIdentity.avatar}
      .messages=${props.messages}
      .toolMessages=${props.toolMessages}
      .stream=${props.stream}
      .reasoningStream=${props.reasoningStream ?? null}
      .streamStartedAt=${props.streamStartedAt}
      .runPhase=${props.runPhase ?? "idle"}
      .a2uiMessages=${props.a2uiMessages ?? []}
      .client=${props.client ?? null}
      .emptyIntro=${emptyIntro}
      @chat-scroll=${(e: CustomEvent<{ event: Event }>) => props.onChatScroll?.(e.detail.event)}
      @open-attachment=${(e: CustomEvent<{ path: string }>) => props.onOpenAttachment?.(e.detail.path)}
      @open-sidebar=${(e: CustomEvent<{ content: string }>) => props.onOpenSidebar?.(e.detail.content)}
      @file-preview=${(e: CustomEvent<{ req: FilePreviewRequest }>) => props.onFilePreview?.(e.detail.req)}
      @a2ui-action=${(e: CustomEvent<{ action: import("@a2ui/web_core/v0_9").A2uiClientAction }>) =>
        props.onA2UIAction?.(e.detail.action)}
    ></openocta-chat-thread>
  `;
  const visibleQueue = props.queue.filter((item) => item.sessionKey === props.sessionKey);

  return html`
    <section class="chat ${isEmptyThread ? "chat-empty" : ""} ${props.focusMode ? "chat--focus" : ""}">
      ${
        props.resourcesPanelOpen && props.onResourcesChange
          ? renderChatResourcesPopover(props, { fixed: true })
          : nothing
      }
      ${
        props.resourcesPanelOpen && props.onResourcesPanelClose
          ? html`<div
              class="chat-resources-backdrop"
              @click=${() => props.onResourcesPanelClose?.()}
            ></div>`
          : nothing
      }
      ${
        props.filePreview
          ? html`<chat-file-preview
              .request=${props.filePreview}
              .onClose=${props.onCloseFilePreview}
            ></chat-file-preview>`
          : nothing
      }
      ${nothing}

      ${props.disabledReason ? html`<div class="callout">${props.disabledReason}</div>` : nothing}

      ${props.error ? html`<div class="callout danger">${props.error}</div>` : nothing}
      ${props.extractSkillError ? html`<div class="callout danger">${props.extractSkillError}</div>` : nothing}

      ${renderCompactionIndicator(props.compactionStatus)}

      ${
        props.focusMode
          ? html`
            <button
              class="chat-focus-exit"
              type="button"
              @click=${props.onToggleFocusMode}
              aria-label="Exit focus mode"
              title="Exit focus mode"
            >
              ${icons.x}
            </button>
          `
          : nothing
      }

      <div class="chat-split-container ${splitSidebarOpen ? "chat-split-container--open" : ""}">
        <div class="chat-main">
          ${thread}
        </div>

        ${
          markdownSidebarOpen
            ? html`
              <div class="chat-sidebar">
                ${renderMarkdownSidebar({
                  content: props.sidebarContent ?? null,
                  error: props.sidebarError ?? null,
                  onClose: props.onCloseSidebar!,
                  onViewRawText: () => {
                    if (!props.sidebarContent || !props.onOpenSidebar) {
                      return;
                    }
                    props.onOpenSidebar(`\`\`\`\n${props.sidebarContent}\n\`\`\``);
                  },
                })}
              </div>
            `
            : nothing
        }
      </div>

      ${
        visibleQueue.length
          ? html`
            <div class="chat-queue" role="status" aria-live="polite">
              <div class="chat-queue__title">Queued (${visibleQueue.length})</div>
              <div class="chat-queue__list">
                ${visibleQueue.map(
                  (item) => html`
                    <div class="chat-queue__item">
                      <div class="chat-queue__text">
                        ${
                          item.text ||
                          (item.attachments?.length
                            ? item.attachments[0]?.filename || "附件"
                            : "")
                        }
                      </div>
                      <button
                        class="btn chat-queue__remove"
                        type="button"
                        aria-label="Remove queued message"
                        @click=${async () => {
                          if (
                            props.confirmQueueRemove &&
                            !(await nativeConfirm(t("chatQueueRemoveConfirm")))
                          ) {
                            return;
                          }
                          props.onQueueRemove(item.id);
                        }}
                      >
                        ${icons.x}
                      </button>
                    </div>
                  `,
                )}
              </div>
            </div>
          `
          : nothing
      }

      ${
        props.showNewMessages
          ? html`
            <button
              class="btn chat-new-messages"
              type="button"
              @click=${props.onScrollToBottom}
            >
              New messages ${icons.arrowDown}
            </button>
          `
          : nothing
      }

      ${renderExtractSkillModal(props)}
      <div class="chat-compose">
        ${renderAttachmentPreview(props)}
        ${
          props.attachmentError
            ? html`<div class="callout danger" style="margin-bottom: 8px;">${props.attachmentError}</div>`
            : nothing
        }
        <div class="chat-compose__inner">
          <label class="field chat-compose__field">
            <span>Message</span>
            <openocta-chat-input
              .disabled=${!canCompose}
              .placeholder=${composePlaceholder}
              .clearToken=${props.composeClearToken ?? 0}
              .insertToken=${props.composeInsertToken ?? 0}
              .insertSnippet=${props.composeInsertSnippet ?? ""}
              .mentionOptions=${(props.localAgents ?? []).filter((a) => a.installed)}
              @compose-send=${(e: CustomEvent<{ message: string }>) => props.onSend(e.detail.message)}
              @compose-draft-change=${(e: CustomEvent<{ hasText: boolean }>) =>
                props.onComposeDraftChange?.(e.detail.hasText)}
              @compose-paste=${(e: CustomEvent<{ event: ClipboardEvent }>) =>
                handlePaste(e.detail.event, props)}
            ></openocta-chat-input>
        </label>
          <div class="chat-compose__row">
          <div class="chat-compose__meta">
            <button
              class="btn btn--icon chat-compose__add-file"
              type="button"
              aria-label="添加文件（图片、文档≤5MB，视频≤50MB）"
              title="添加文件（图片、文档≤5MB，视频 MP4/MOV/AVI≤50MB，不支持压缩包）"
              ?disabled=${!canCompose || !props.onAttachmentsChange}
              @click=${() => {
                const input = document.getElementById("chat-file-input") as HTMLInputElement | null;
                input?.click();
              }}
            >
              ${icons.plus}
            </button>
            <input
              id="chat-file-input"
              type="file"
              accept=${CHAT_ATTACHMENT_ACCEPT}
              style="display:none"
              @change=${(e: Event) => handleFilePick(e, props)}
            />
            ${
              props.onResourcesChange
                ? html`
                    <div class="chat-resources-anchor">
                      <button
                        type="button"
                        class="chat-compose__chip ${props.resourcesPanelOpen ? "chat-compose__chip--active" : ""} ${chatResourcesSelectionCount(props.resources ?? { configured: false, skillKeys: [], mcpServers: [], webSearch: true }) > 0 ? "chat-compose__chip--active" : ""}"
                        aria-label="全部资源"
                        aria-expanded=${props.resourcesPanelOpen ? "true" : "false"}
                        title="选择 Skill / MCP"
                        ?disabled=${!props.connected}
                        ${ref(resourcesAnchorRef(props))}
                        @click=${(e: Event) => {
                          e.stopPropagation();
                          props.onResourcesPanelToggle?.();
                        }}
                      >
                        <span class="chat-compose__chip-icon" aria-hidden="true">${icons.overviewGrid}</span>
                        <span class="chat-compose__chip-label">全部资源</span>
                        ${
                          chatResourcesSelectionCount(
                            props.resources ?? {
                              configured: false,
                              skillKeys: [],
                              mcpServers: [],
                              webSearch: true,
                            },
                          ) > 0
                            ? html`<span class="chat-compose__chip-badge">${chatResourcesSelectionCount(props.resources!)}</span>`
                            : nothing
                        }
                      </button>
                    </div>
                  `
                : nothing
            }
            ${
              (props.localAgents?.length ?? 0) > 0
                ? html`
                    <openocta-local-agent-picker
                      .agents=${props.localAgents ?? []}
                      ?disabled=${!canCompose}
                      @agent-insert=${(e: CustomEvent<{ mention: string }>) =>
                        props.onComposeInsert?.(e.detail.mention)}
                    ></openocta-local-agent-picker>
                  `
                : nothing
            }
            ${
              props.onModelRefChange
                ? html`
                    <label class="field chat-compose__model-select">
                      <span class="select small"><select
                        aria-label="大模型"
                        .value=${props.modelRef ?? ""}
                        ?disabled=${!props.connected}
                        @change=${(e: Event) => {
                          const value = (e.target as HTMLSelectElement).value.trim();
                          props.onModelRefChange?.(value === "" ? null : value);
                        }}
                      >
                        ${(props.modelOptions ?? [{ value: "", label: "默认" }]).map(
                          (opt) => html`<option value=${opt.value}>${opt.label}</option>`,
                        )}
                      </select></span>
                    </label>
                  `
                : nothing
            }
            ${
              props.canExtractSkill && props.onExtractSkill
                ? html`
                    <button
                      type="button"
                      class="chat-compose__chip chat-compose__chip--extract ${extractingSkill ? "chat-compose__chip--loading" : ""}"
                      aria-label="提炼 SKILL"
                      aria-busy=${extractingSkill ? "true" : "false"}
                      title=${extractingSkill ? "正在提炼 Skill…" : "从对话历史提炼 Skill"}
                      ?disabled=${!props.connected || extractingSkill}
                      @click=${() => props.onExtractSkill?.()}
                    >
                      <span class="chat-compose__chip-icon" aria-hidden="true">
                        ${extractingSkill ? icons.loader2 : icons.book}
                      </span>
                      <span class="chat-compose__chip-label">提炼SKILL</span>
                    </button>
                  `
                : nothing
            }
          </div>
          <div class="chat-compose__actions">
            <button
              class="btn chat-compose__secondary"
              ?disabled=${!props.connected || (!canAbort && props.sending)}
              @click=${canAbort ? props.onAbort : props.onNewSession}
            >
              ${canAbort ? "停止" : "新会话"}
            </button>
            <button
              class="btn chat-compose__send"
              type="button"
              aria-label="发送"
              title="发送 (Enter)"
              ?disabled=${!canSubmit}
              @click=${(e: Event) => {
                const compose = (e.currentTarget as HTMLElement).closest(".chat-compose");
                const input = compose?.querySelector("openocta-chat-input") as
                  | { requestSend?: () => void }
                  | null;
                input?.requestSend?.();
              }}
            >
              ${isBusy ? icons.loader2 : icons.send}
            </button>
          </div>
          </div>
        </div>
      </div>

      ${emptyPrompts}
    </section>
  `;
}
