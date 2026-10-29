import { describe, expect, it, vi } from "vitest";
import { OpenClawApp } from "./app.ts";
import { TerminalAssistantController } from "./terminal-assistant-state.ts";
import type { ReactiveControllerHost } from "lit";
import type { GatewayBrowserClient } from "./gateway.ts";

describe("terminal app send binding", () => {
  it("sends the visible SSH session through the real app handler and chat controller", async () => {
    const request = vi.fn().mockResolvedValue({ runId: "run-test" });
    const assistant = new TerminalAssistantController({ addController: vi.fn(), requestUpdate: vi.fn() } as unknown as ReactiveControllerHost);
    const app = {
      terminalHosts: [{ id: "host-a", host: "127.0.0.1", username: "fixture", connected: true, sessionId: "ssh-visible" }],
      terminalActiveHostId: "host-a",
      terminalAssistant: assistant,
      terminalSessionView: { getRecentOutput: () => "/tmp" },
      client: { request } as unknown as GatewayBrowserClient,
      connected: true,
      requestUpdate: vi.fn(),
    };
    await OpenClawApp.prototype.handleTerminalAssistantSend.call(app as unknown as OpenClawApp, "执行 pwd");
    expect(request).toHaveBeenCalledWith("chat.send", expect.objectContaining({
      sessionKey: expect.stringMatching(/^custom:terminal-/),
      terminalSessionId: "ssh-visible",
      message: expect.stringContaining("ssh-visible"),
    }));
    expect(assistant.active).toBe(assistant.forHost("host-a"));
  });
});
