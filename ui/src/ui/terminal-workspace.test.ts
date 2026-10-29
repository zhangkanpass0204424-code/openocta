import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "lit";
import type { ReactiveControllerHost } from "lit";
import type { AppViewState } from "./app-view-state.ts";
import { TerminalWorkspaceController } from "./terminal-workspace-state.ts";
import { TerminalAssistantController } from "./terminal-assistant-state.ts";
import { renderTerminalWorkspace } from "./terminal-workspace.ts";

const containers: HTMLElement[] = [];
afterEach(() => { containers.splice(0).forEach((node) => node.remove()); vi.restoreAllMocks(); });

function setup() {
  const controller = new TerminalWorkspaceController({ addController: vi.fn(), requestUpdate: vi.fn() } as unknown as ReactiveControllerHost);
  controller.hosts = [{ id: "a", host: "192.0.2.1", port: 22, username: "root", fingerprint: null, connected: true, sessionId: "s1" }];
  const state = {
    terminalAssistant: new TerminalAssistantController({ addController: vi.fn(), requestUpdate: vi.fn() } as unknown as ReactiveControllerHost),
    activeWorkspace: "terminal", terminalWorkspace: controller,
    terminalActiveHostId: "a", terminalSection: "主机", terminalAssistantWidth: 360,
    terminalSidebarCollapsed: false, terminalContextPickerOpen: false,
    terminalAssistantDraft: "", chatMessages: [], chatAttachments: [],
    chatResources: { configured: false, skillKeys: [], mcpServers: [], webSearch: true },
    settings: { gatewayUrl: "ws://localhost:19000" }, connected: true,
    get terminalHosts() { return controller.hosts; },
    get terminalCommandDraft() { return controller.command.text; },
    get terminalCommandTargetHostId() { return controller.command.hostId; },
  } as unknown as AppViewState;
  const container = document.createElement("div");
  document.body.append(container); containers.push(container);
  const update = () => render(renderTerminalWorkspace(state), container);
  update();
  return { state, controller, container, update };
}

describe("terminal workspace layout interactions", () => {
  it("renders three panes and folds the resource sidebar", () => {
    const { state, container, update } = setup();
    expect(container.querySelector(".terminal-resource-sidebar")).not.toBeNull();
    expect(container.querySelector(".terminal-center-pane")).not.toBeNull();
    expect(container.querySelector(".terminal-ai-panel")).not.toBeNull();
    container.querySelector<HTMLButtonElement>(".terminal-sidebar-toggle")!.click();
    expect(state.terminalSidebarCollapsed).toBe(true);
    update();
    expect(container.querySelector(".terminal-app--sidebar-collapsed")).not.toBeNull();
  });

  it("opens the host context picker when typing @", () => {
    const { state, container, update } = setup();
    const input = container.querySelector<HTMLTextAreaElement>("textarea")!;
    input.value = "@";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    expect(state.terminalContextPickerOpen).toBe(true);
    update();
    expect(container.querySelector(".terminal-context-picker")?.textContent).toContain("192.0.2.1");
  });

  it("disables command execution after reconnect until explicitly rebound", () => {
    const { controller, container, update } = setup();
    controller.proposeCommand("hostname", "a");
    controller.hosts = controller.hosts.map((h) => ({ ...h, sessionId: "s2" }));
    update();
    const buttons = [...container.querySelectorAll<HTMLButtonElement>(".terminal-command-draft button")];
    expect(buttons.find((b) => b.textContent?.includes("发送到目标"))?.disabled).toBe(true);
    buttons.find((b) => b.textContent?.includes("绑定当前主机"))!.click();
    update();
    expect(controller.resolveCommandTarget()?.sessionId).toBe("s2");
  });

  it("disables assistant input when gateway disconnects", () => {
    const { state, container, update } = setup();
    state.connected = false; update();
    expect(container.querySelector<HTMLTextAreaElement>("textarea")!.disabled).toBe(true);
  });
});
