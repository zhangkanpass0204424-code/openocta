import type { AppViewState } from "./app-view-state.ts";

// Capture one conversation per render so asynchronous attachment reads cannot switch targets.
export function terminalAssistantView(app: AppViewState): AppViewState {
  const controller = app.terminalAssistant;
  controller.selectHost(app.terminalActiveHostId);
  const conversation = controller.active;
  const aliases: Record<string, string> = {
    terminalAssistantDraft: "chatMessage", chatModelRef: "modelRef",
    chatResources: "resources", chatAttachmentError: "attachmentError",
  };
  const fields = new Set([
    "chatMessages", "chatStream", "chatReasoningStream", "chatRunId", "chatRunPhase",
    "chatSending", "chatAttachments", "chatLoading",
  ]);
  return new Proxy(app, {
    get(target, property) {
      if (typeof property === "string" && (fields.has(property) || property in aliases)) {
        return Reflect.get(conversation, aliases[property] ?? property);
      }
      const value = Reflect.get(target, property);
      return typeof value === "function" ? value.bind(target) : value;
    },
    set(target, property, value) {
      if (typeof property === "string" && (fields.has(property) || property in aliases)) {
        Reflect.set(conversation, aliases[property] ?? property, value);
        controller.notify();
        return true;
      }
      return Reflect.set(target, property, value, target);
    },
  });
}
