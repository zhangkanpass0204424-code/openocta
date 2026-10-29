import { describe, expect, it, vi } from "vitest";
import type { ReactiveControllerHost } from "lit";
import type { GatewayBrowserClient } from "./gateway.ts";
import { TerminalAssistantController } from "./terminal-assistant-state.ts";
import { terminalAssistantView } from "./terminal-assistant-view.ts";
import type { AppViewState } from "./app-view-state.ts";

function setup() {
  const request = vi.fn().mockResolvedValue({ messages: [] });
  const controller = new TerminalAssistantController({ addController: vi.fn(), requestUpdate: vi.fn() } as unknown as ReactiveControllerHost);
  controller.selectHost("a");
  controller.syncConnection({ request } as unknown as GatewayBrowserClient, true);
  return { controller, request };
}

describe("terminal assistant isolation", () => {
  it("preserves separate drafts, models, skills and histories per host", () => {
    const { controller } = setup();
    const first = controller.active;
    first.chatMessage = "draft a";
    first.modelRef = "provider/model";
    first.resources.skillKeys = ["skill-a"];
    first.chatMessages = [{ role: "user", content: "host a" }];
    controller.selectHost("b");
    expect(controller.active.sessionKey).not.toBe(first.sessionKey);
    expect(controller.active.chatMessage).toBe("");
    expect(controller.active.modelRef).toBeNull();
    expect(controller.active.resources.skillKeys).toEqual([]);
    expect(controller.active.chatMessages).toEqual([]);
    controller.selectHost("a");
    expect(controller.active).toBe(first);
    expect(controller.active.chatMessage).toBe("draft a");
  });

  it("routes background stream to its own host only", () => {
    const { controller } = setup();
    const first = controller.active;
    controller.selectHost("b");
    expect(controller.receive({ sessionKey: first.sessionKey, state: "delta", text: "answer a", runId: "run-a" })).toBe(true);
    expect(first.chatStream).toBe("answer a");
    expect(controller.active.chatStream).toBeNull();
    expect(controller.receive({ sessionKey: "agent.main.main", state: "delta", text: "ordinary chat" })).toBe(false);
  });

  it("uses a dedicated session and prevents concurrent sends", async () => {
    const { controller, request } = setup();
    expect(await controller.send("hello", "ssh-session-a")).toBe(true);
        expect(request).toHaveBeenCalledWith("chat.send", expect.objectContaining({ sessionKey: controller.active.sessionKey, message: "hello", terminalSessionId: "ssh-session-a" }));
    expect(await controller.send("second")).toBe(false);
    expect(request).toHaveBeenCalledTimes(1);
  });

  it("keeps asynchronous view writes in the original conversation", () => {
    const { controller } = setup();
    const app = { terminalAssistant: controller, terminalActiveHostId: "a", chatMessages: ["ordinary"], chatAttachments: [] } as unknown as AppViewState;
    const view = terminalAssistantView(app);
    controller.selectHost("b");
    view.chatAttachmentError = "attachment failed";
    view.chatMessages = ["terminal"];
    expect(controller.forHost("a").attachmentError).toBe("attachment failed");
    expect(controller.active.attachmentError).toBeNull();
    expect(app.chatMessages).toEqual(["ordinary"]);
  });

  it("starts a new conversation without resetting another host", async () => {
    const { controller } = setup();
    const oldKey = controller.active.sessionKey;
    controller.forHost("b").chatMessage = "keep b";
    await controller.newConversation();
    expect(controller.active.sessionKey).not.toBe(oldKey);
    expect(controller.owns(oldKey)).toBe(true);
    expect(controller.receive({ sessionKey: oldKey, state: "delta", text: "late answer", runId: "late-run" })).toBe(true);
    expect(controller.active.chatStream).toBeNull();
    expect(controller.forHost("b").chatMessage).toBe("keep b");
  });

  it("aborts only the active host conversation", async () => {
    const { controller, request } = setup();
    const first = controller.active;
    first.chatRunId = "run-a";
    controller.selectHost("b");
    controller.syncConnection(first.client, true);
    controller.active.chatRunId = "run-b";
    await controller.stop();
    expect(request).toHaveBeenCalledWith("chat.abort", expect.objectContaining({ sessionKey: controller.active.sessionKey, runId: "run-b" }));
    expect(first.chatRunId).toBe("run-a");
  });

  it("does not send when disconnected and retains attachments on failure", async () => {
    const { controller, request } = setup();
    controller.active.connected = false;
    expect(await controller.send("question")).toBe(false);
    controller.active.connected = true;
    request.mockRejectedValueOnce(new Error("offline"));
    expect(await controller.send("question")).toBe(false);
    expect(controller.active.lastError).toContain("offline");
  });
});
