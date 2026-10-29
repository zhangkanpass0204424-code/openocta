import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";

type TerminalOutput = {
  data?: string;
  offset?: number;
  closed?: boolean;
  error?: string;
};

export class TerminalSessionView {
  private terminal: Terminal | null = null;
  private fitAddon: FitAddon | null = null;
  private resizeObserver: ResizeObserver | null = null;
  private pollController: AbortController | null = null;
  private sessionId: string | null = null;
  private apiBase = "";
  private inputQueue = "";
  private sendingInput = false;

  mount(
    element: HTMLElement,
    apiBase: string,
    sessionId: string,
    onClosed: (message: string) => void,
  ) {
    if (
      this.terminal &&
      this.sessionId === sessionId &&
      this.apiBase === apiBase &&
      this.terminal.element?.parentElement === element
    ) {
      this.fitAddon?.fit();
      return;
    }
    this.dispose();
    this.sessionId = sessionId;
    this.apiBase = apiBase;
    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: 'Consolas, "Cascadia Mono", "Courier New", monospace',
      fontSize: 13,
      scrollback: 5000,
      theme: {
        background: "#111923",
        foreground: "#e2eaf2",
        cursor: "#f3f6f9",
        selectionBackground: "#3b526b",
      },
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(element);
    this.terminal = terminal;
    this.fitAddon = fitAddon;
    fitAddon.fit();
    terminal.focus();

    terminal.onData((data) => {
      this.inputQueue += data;
      if (this.inputQueue.length > 64 * 1024) {
        this.inputQueue = "";
        onClosed("终端输入积压过多，已断开输入通道");
        return;
      }
      void this.flushInput(apiBase, sessionId, onClosed);
    });
    terminal.onResize(({ cols, rows }) => {
      void fetch(`${apiBase}/api/terminal/sessions/${encodeURIComponent(sessionId)}/resize`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cols, rows }),
      }).then((response) => {
        if (!response.ok) throw new Error(`终端尺寸同步失败（HTTP ${response.status}）`);
      }).catch((error: unknown) => {
        if (this.sessionId === sessionId) {
          onClosed(error instanceof Error ? error.message : "终端尺寸同步失败");
        }
      });
    });

    this.resizeObserver = new ResizeObserver(() => {
      if (this.sessionId !== sessionId) return;
      fitAddon.fit();
    });
    this.resizeObserver.observe(element);

    const controller = new AbortController();
    this.pollController = controller;
    void this.readOutput(apiBase, sessionId, controller.signal, onClosed);
  }

  dispose() {
    this.pollController?.abort();
    this.pollController = null;
    this.resizeObserver?.disconnect();
    this.resizeObserver = null;
    this.terminal?.dispose();
    this.terminal = null;
    this.fitAddon = null;
    this.sessionId = null;
    this.apiBase = "";
    this.inputQueue = "";
    this.sendingInput = false;
  }

  getRecentOutput(maxLines = 80): string {
    const buffer = this.terminal?.buffer.active;
    if (!buffer) return "";
    const end = buffer.baseY + buffer.cursorY;
    const start = Math.max(0, end - maxLines + 1);
    const lines: string[] = [];
    for (let index = start; index <= end; index += 1) {
      const line = buffer.getLine(index)?.translateToString(true);
      if (line) lines.push(line);
    }
    return lines.join("\n").slice(-8000);
  }

  private async readOutput(
    apiBase: string,
    sessionId: string,
    signal: AbortSignal,
    onClosed: (message: string) => void,
  ) {
    let offset = 0;
    const terminal = this.terminal;
    if (!terminal) return;
    try {
      while (!signal.aborted && this.sessionId === sessionId) {
        const response = await fetch(
          `${apiBase}/api/terminal/sessions/${encodeURIComponent(sessionId)}/output?offset=${offset}`,
          { signal },
        );
        if (!response.ok) throw new Error(`终端连接已断开（HTTP ${response.status}）`);
        const output = (await response.json()) as TerminalOutput;
        if (this.sessionId !== sessionId || signal.aborted) return;
        if (typeof output.data === "string" && output.data) terminal.write(output.data);
        if (typeof output.offset === "number") offset = output.offset;
        if (output.closed) {
          onClosed(output.error || "SSH 会话已关闭");
          return;
        }
        await new Promise<void>((resolve) => window.setTimeout(resolve, 100));
      }
    } catch (error) {
      if (signal.aborted || this.sessionId !== sessionId) return;
      onClosed(error instanceof Error ? error.message : "读取 SSH 输出失败");
    }
  }

  private async flushInput(
    apiBase: string,
    sessionId: string,
    onClosed: (message: string) => void,
  ) {
    if (this.sendingInput) return;
    this.sendingInput = true;
    try {
      while (this.inputQueue && this.sessionId === sessionId) {
        const data = this.inputQueue.slice(0, 4096);
        this.inputQueue = this.inputQueue.slice(data.length);
        const response = await fetch(
          `${apiBase}/api/terminal/sessions/${encodeURIComponent(sessionId)}/input`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ data }),
          },
        );
        if (response.status === 409) {
          this.inputQueue = "";
          this.terminal?.write("\r\n[输入被拒绝：AI 命令正在执行，或上次执行超时后需重新连接]\r\n");
          return;
        }
        if (!response.ok) throw new Error(`终端输入失败（HTTP ${response.status}）`);
      }
    } catch (error) {
      onClosed(error instanceof Error ? error.message : "发送终端输入失败");
    } finally {
      this.sendingInput = false;
      if (this.inputQueue && this.sessionId === sessionId) {
        void this.flushInput(apiBase, sessionId, onClosed);
      }
    }
  }
}
