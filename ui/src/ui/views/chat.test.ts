import { render } from "lit";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionsListResult } from "../types.ts";
const nativeConfirmMock = vi.hoisted(() => vi.fn());
vi.mock("../native-dialog-bridge.ts", () => ({
  nativeConfirm: nativeConfirmMock,
}));
import {
  isChatRunActive,
  loadAttachmentFromFile,
  renderChat,
  validateChatAttachmentFile,
  CHAT_ATTACHMENT_MAX_BYTES,
  CHAT_ATTACHMENT_VIDEO_MAX_BYTES,
  type ChatProps,
} from "./chat.ts";

function createSessions(): SessionsListResult {
  return {
    ts: 0,
    path: "",
    count: 0,
    defaults: { model: null, contextTokens: null },
    sessions: [],
  };
}

function createProps(overrides: Partial<ChatProps> = {}): ChatProps {
  return {
    sessionKey: "main",
    onSessionKeyChange: () => undefined,
    thinkingLevel: null,
    showThinking: false,
    loading: false,
    sending: false,
    canAbort: false,
    compactionStatus: null,
    messages: [],
    toolMessages: [],
    stream: null,
    streamStartedAt: null,
    assistantAvatarUrl: null,
    draft: "",
    queue: [],
    connected: true,
    canSend: true,
    disabledReason: null,
    error: null,
    sessions: createSessions(),
    focusMode: false,
    assistantName: "OpenClaw",
    assistantAvatar: null,
    onRefresh: () => undefined,
    onToggleFocusMode: () => undefined,
    onDraftChange: () => undefined,
    onSend: () => undefined,
    onQueueRemove: () => undefined,
    confirmQueueRemove: false,
    onNewSession: () => undefined,
    ...overrides,
  };
}

describe("chat view", () => {
  beforeEach(() => {
    nativeConfirmMock.mockReset();
    document.documentElement.lang = "en";
  });

  it("confirms queued message removal on the message page", async () => {
    nativeConfirmMock.mockResolvedValueOnce(true);
    const container = document.createElement("div");
    const onQueueRemove = vi.fn();
    render(
      renderChat(
        createProps({
          queue: [{ id: "q-1", sessionKey: "main", text: "queued", attachments: [] }],
          onQueueRemove,
          confirmQueueRemove: true,
        }),
      ),
      container,
    );

    const removeButton = container.querySelector<HTMLButtonElement>(".chat-queue__remove");
    removeButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await Promise.resolve();

    expect(nativeConfirmMock).toHaveBeenCalledWith("Remove this queued message?");
    expect(onQueueRemove).toHaveBeenCalledWith("q-1");
  });

  it("keeps attachment removal direct without a confirmation prompt", () => {
    const container = document.createElement("div");
    const onAttachmentsChange = vi.fn();
    render(
      renderChat(
        createProps({
          attachments: [
            {
              id: "att-1",
              dataUrl: "data:image/png;base64,abc",
              mimeType: "image/png",
              filename: "demo.png",
              sizeBytes: 12,
              kind: "image",
            },
          ],
          onAttachmentsChange,
        }),
      ),
      container,
    );

    const removeButton = container.querySelector<HTMLButtonElement>(".chat-attachment__remove");
    removeButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));

    expect(nativeConfirmMock).not.toHaveBeenCalled();
    expect(onAttachmentsChange).toHaveBeenCalledWith([]);
  });

  it("uses chat-empty on the section when the thread is empty", () => {
    const container = document.createElement("div");
    render(renderChat(createProps()), container);

    expect(container.querySelector("section.chat.chat-empty")).not.toBeNull();
    expect(container.querySelector("div.chat-empty")).toBeNull();
    expect(container.querySelector("openocta-chat-suggestions")).not.toBeNull();
    expect(container.textContent).toContain("推荐");
  });

  it("renders scenario quick prompts when provided", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          quickPrompts: ["执行主机巡检", "查看磁盘使用率"],
        }),
      ),
      container,
    );

    expect(container.textContent).toContain("执行主机巡检");
    expect(container.textContent).toContain("查看磁盘使用率");
    expect(container.textContent).not.toContain("MySQL 告警分析报告");
  });

  it("disables send when there is no draft content", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          draft: "   ",
          attachments: [],
        }),
      ),
      container,
    );

    const sendButton = container.querySelector<HTMLButtonElement>(".chat-compose__send");
    expect(sendButton?.disabled).toBe(true);
  });

  it("keeps send enabled when attachments exist without draft text", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          draft: "   ",
          attachments: [
            {
              id: "att-1",
              dataUrl: "data:image/png;base64,abc",
              mimeType: "image/png",
              filename: "demo.png",
              sizeBytes: 12,
              kind: "image",
            },
          ],
        }),
      ),
      container,
    );

    const sendButton = container.querySelector<HTMLButtonElement>(".chat-compose__send");
    expect(sendButton?.disabled).toBe(false);
  });

  it("shows attachment validation errors inline", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          attachmentError: "不支持压缩包或可执行文件",
        }),
      ),
      container,
    );

    expect(container.textContent).toContain("不支持压缩包或可执行文件");
  });

  it("shows a live activity indicator while a run is waiting between tool turns", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          canAbort: true,
          runPhase: "tool",
          stream: null,
          a2uiMessages: [],
          messages: [
            {
              role: "assistant",
              stopReason: "tool_use",
              content: [{ type: "toolCall", name: "ls", id: "ls:0" }],
            },
          ],
        }),
      ),
      container,
    );

    expect(container.querySelector(".chat-agent-activity--tool")).not.toBeNull();
    expect(container.querySelector(".chat-avatar-spinner")).toBeNull();
  });

  it("does not show a reading indicator after run ends with empty stream placeholder", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          canAbort: false,
          stream: "",
          messages: [{ role: "assistant", content: [{ type: "text", text: "done" }] }],
        }),
      ),
      container,
    );

    expect(container.querySelector(".chat-reading-indicator")).toBeNull();
    expect(isChatRunActive(createProps({ canAbort: false, stream: "", sending: false }))).toBe(false);
  });

  it("shows a stop button when aborting is available", () => {
    const container = document.createElement("div");
    const onAbort = vi.fn();
    render(
      renderChat(
        createProps({
          canAbort: true,
          onAbort,
        }),
      ),
      container,
    );

    const stopButton = Array.from(container.querySelectorAll("button")).find(
      (btn) => btn.textContent?.trim() === "停止",
    );
    expect(stopButton).not.toBeUndefined();
    stopButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(onAbort).toHaveBeenCalledTimes(1);
    expect(container.textContent).not.toContain("新会话");
  });

  it("does not close the resources popover when clicking inside it", () => {
    const container = document.createElement("div");
    const onResourcesPanelClose = vi.fn();
    const onResourcesChange = vi.fn();
    render(
      renderChat(
        createProps({
          resourcesPanelOpen: true,
          resources: { configured: false, skillKeys: [], mcpServers: [], webSearch: true },
          onResourcesChange,
          onResourcesPanelClose,
          onResourcesPanelToggle: vi.fn(),
          resourceSkillOptions: [
            {
              name: "Demo Skill",
              description: "demo",
              source: "workspace",
              filePath: "/tmp/SKILL.md",
              baseDir: "/tmp",
              skillKey: "demo-skill",
              always: false,
              disabled: false,
              blockedByAllowlist: false,
              eligible: true,
              requirements: { bins: [], env: [], config: [], os: [] },
              missing: { bins: [], env: [], config: [], os: [] },
              configChecks: [],
              install: [],
            },
          ],
        }),
      ),
      container,
    );

    const checkbox = container.querySelector<HTMLInputElement>(
      ".chat-resources-popover__item input[type='checkbox']",
    );
    expect(checkbox).not.toBeNull();
    checkbox?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(onResourcesPanelClose).not.toHaveBeenCalled();
    expect(onResourcesChange).toHaveBeenCalled();
  });

  it("closes the resources popover when clicking the backdrop", () => {
    const container = document.createElement("div");
    const onResourcesPanelClose = vi.fn();
    render(
      renderChat(
        createProps({
          resourcesPanelOpen: true,
          resources: { configured: false, skillKeys: [], mcpServers: [], webSearch: true },
          onResourcesChange: vi.fn(),
          onResourcesPanelClose,
          onResourcesPanelToggle: vi.fn(),
        }),
      ),
      container,
    );

    container.querySelector(".chat-resources-backdrop")?.dispatchEvent(
      new MouseEvent("click", { bubbles: true }),
    );
    expect(onResourcesPanelClose).toHaveBeenCalledTimes(1);
  });

  it("shows a new session button when aborting is unavailable", () => {
    const container = document.createElement("div");
    const onNewSession = vi.fn();
    render(
      renderChat(
        createProps({
          canAbort: false,
          onNewSession,
        }),
      ),
      container,
    );

    const newSessionButton = Array.from(container.querySelectorAll("button")).find(
      (btn) => btn.textContent?.trim() === "新会话",
    );
    expect(newSessionButton).not.toBeUndefined();
    newSessionButton?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(onNewSession).toHaveBeenCalledTimes(1);
    expect(container.textContent).not.toContain("停止");
  });

  it("renders live A2UI messages during an active run", () => {
    const container = document.createElement("div");
    render(
      renderChat(
        createProps({
          canAbort: true,
          a2uiMessages: [
            {
              version: "v0.9",
              createSurface: { surfaceId: "main", catalogId: "basic" },
            },
            {
              version: "v0.9",
              updateComponents: {
                surfaceId: "main",
                components: [{ id: "root", component: "Text", text: "Hello A2UI" }],
              },
            },
          ],
        }),
      ),
      container,
    );

    expect(container.textContent).toContain("Hello A2UI");
  });

  it("renders A2UI file attachments with preview and download actions", () => {
    const container = document.createElement("div");
    const onFilePreview = vi.fn();
    render(
      renderChat(
        createProps({
          canAbort: true,
          a2uiMessages: [
            {
              version: "v0.9",
              createSurface: { surfaceId: "main", catalogId: "basic" },
            },
            {
              version: "v0.9",
              updateComponents: {
                surfaceId: "main",
                components: [
                  {
                    id: "root",
                    component: "Text",
                    text: '文件已就绪\n@@OPENOCTA_ATTACHMENTS@@\n[{"type":"file","filename":"demo.txt","mimeType":"text/plain","data":"aGk="}]',
                  },
                ],
              },
            },
          ],
          onFilePreview,
        }),
      ),
      container,
    );

    expect(container.textContent).toContain("demo.txt");
    expect(container.querySelector(".chat-file-preview-wrap, .chat-file-card--compact")).not.toBeNull();
    expect(container.querySelector(".chat-file-icon-btn")).not.toBeNull();
    const previewBtn = container.querySelector<HTMLButtonElement>(".chat-file-icon-btn[title='预览']");
    expect(previewBtn).not.toBeNull();
    previewBtn?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(onFilePreview).toHaveBeenCalled();
  });
});

describe("chat attachment validation", () => {
  it("loads a validated file as a chat attachment", async () => {
    const attachment = await loadAttachmentFromFile(
      new File(["hello"], "notes.txt", { type: "text/plain" }),
    );

    expect(attachment.filename).toBe("notes.txt");
    expect(attachment.mimeType).toBe("text/plain");
    expect(attachment.sizeBytes).toBe(5);
    expect(attachment.dataUrl).toBe("data:text/plain;base64,aGVsbG8=");
  });

  it("rejects archives and oversize files", () => {
    expect(
      validateChatAttachmentFile({ name: "demo.zip", type: "application/zip", size: 100 } as File).ok,
    ).toBe(false);
    expect(
      validateChatAttachmentFile({
        name: "demo.pdf",
        type: "application/pdf",
        size: CHAT_ATTACHMENT_MAX_BYTES + 1,
      } as File).ok,
    ).toBe(false);
    expect(
      validateChatAttachmentFile({ name: "notes.txt", type: "text/plain", size: 128 } as File).ok,
    ).toBe(true);
    expect(
      validateChatAttachmentFile({
        name: "clip.mp4",
        type: "video/mp4",
        size: CHAT_ATTACHMENT_VIDEO_MAX_BYTES,
      } as File).ok,
    ).toBe(true);
    expect(
      validateChatAttachmentFile({
        name: "clip.mp4",
        type: "video/mp4",
        size: CHAT_ATTACHMENT_VIDEO_MAX_BYTES + 1,
      } as File).ok,
    ).toBe(false);
  });
});
