import type { ReactiveController, ReactiveControllerHost } from "lit";

export type TerminalHostResource = {
  id: string;
  host: string;
  port: number;
  username: string;
  fingerprint: string | null;
};

export type TerminalHost = TerminalHostResource & {
  connected: boolean;
  sessionId: string | null;
};

export type CommandProposal = {
  text: string;
  hostId: string | null;
  sessionId: string | null;
};

export class TerminalWorkspaceController implements ReactiveController {
  private resources: TerminalHostResource[] = [];
  private cachedHosts: TerminalHost[] = [];
  private sessions = new Map<string, { connected: boolean; sessionId: string | null }>();
  private draft = "";
  private proposal: CommandProposal = { text: "", hostId: null, sessionId: null };

  constructor(private readonly host: ReactiveControllerHost & { requestUpdate(name?: PropertyKey, oldValue?: unknown): void }) {
    host.addController(this);
  }

  hostDisconnected() {
    this.draft = "";
    this.clearCommand();
  }

  get hosts(): TerminalHost[] {
    return this.cachedHosts;
  }

  set hosts(hosts: TerminalHost[]) {
    const previous = this.hosts;
    this.resources = hosts.map(({ connected: _connected, sessionId: _sessionId, ...resource }) => resource);
    this.sessions = new Map(hosts.map(({ id, connected, sessionId }) => [id, { connected, sessionId }]));
    this.cachedHosts = this.resources.map((resource) => ({
      ...resource,
      ...this.sessions.get(resource.id)!,
    }));
    this.host.requestUpdate("terminalHosts", previous);
  }

  get assistantDraft() { return this.draft; }
  set assistantDraft(value: string) {
    this.draft = value;
    this.host.requestUpdate();
  }

  get command(): Readonly<CommandProposal> { return this.proposal; }

  proposeCommand(text: string, hostId: string | null) {
    const target = this.hosts.find((item) => item.id === hostId);
    this.proposal = { text, hostId, sessionId: target?.connected ? target.sessionId : null };
    this.host.requestUpdate();
  }

  clearCommand() {
    this.proposal = { text: "", hostId: null, sessionId: null };
    this.host.requestUpdate();
  }

  resolveCommandTarget(): TerminalHost | null {
    const target = this.hosts.find((item) => item.id === this.proposal.hostId);
    return target?.connected && this.proposal.sessionId && target.sessionId === this.proposal.sessionId
      ? target : null;
  }
}
