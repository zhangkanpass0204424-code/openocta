import { describe, expect, it, vi } from "vitest";
import type { ReactiveControllerHost } from "lit";
import { TerminalWorkspaceController, type TerminalHost } from "./terminal-workspace-state.ts";

function setup() {
  const host = { addController: vi.fn(), requestUpdate: vi.fn() };
  const state = new TerminalWorkspaceController(host as unknown as ReactiveControllerHost);
  const first: TerminalHost = {
    id: "a", host: "192.0.2.1", port: 22, username: "root",
    fingerprint: "fingerprint", connected: true, sessionId: "session-a",
  };
  state.hosts = [first, { ...first, id: "b", host: "192.0.2.2", sessionId: "session-b" }];
  return { state, host };
}

describe("terminal workspace state", () => {
  it("notifies Lit when host sessions change", () => {
    const { state, host } = setup();
    expect(state.hosts).toHaveLength(2);
    expect(state.hosts).toBe(state.hosts);
    expect(host.requestUpdate).toHaveBeenCalledWith("terminalHosts", []);
  });

  it("keeps commands bound to the original host and session", () => {
    const { state } = setup();
    state.proposeCommand("hostname", "a");
    state.hosts = [...state.hosts].reverse();
    expect(state.resolveCommandTarget()?.sessionId).toBe("session-a");
  });

  it("rejects a proposal after disconnect", () => {
    const { state } = setup();
    state.proposeCommand("hostname", "a");
    state.hosts = state.hosts.map((h) => h.id === "a" ? { ...h, connected: false, sessionId: null } : h);
    expect(state.resolveCommandTarget()).toBeNull();
  });

  it("does not run an old command on a reconnected session", () => {
    const { state } = setup();
    state.proposeCommand("hostname", "a");
    state.hosts = state.hosts.map((h) => h.id === "a" ? { ...h, sessionId: "new-session" } : h);
    expect(state.resolveCommandTarget()).toBeNull();
    state.proposeCommand("hostname", "a");
    expect(state.resolveCommandTarget()?.sessionId).toBe("new-session");
  });

  it("requires explicit binding when created without a host", () => {
    const { state } = setup();
    state.proposeCommand("hostname", null);
    expect(state.resolveCommandTarget()).toBeNull();
    expect(state.command.hostId).toBeNull();
  });

  it("clears drafts without modifying host resources", () => {
    const { state } = setup();
    state.assistantDraft = "question";
    state.proposeCommand("hostname", "a");
    state.hostDisconnected();
    expect(state.assistantDraft).toBe("");
    expect(state.command.text).toBe("");
    expect(state.hosts).toHaveLength(2);
  });
});
