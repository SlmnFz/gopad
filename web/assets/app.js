import { RgaDocument, createTextOperations, encodeOperation } from "./crdt.js";
import {
  anchorForVisibleOffset,
  PresenceState,
  renderRemoteCursors,
} from "./presence.js";
import { renderAvatar } from "./avatar.js";
import { SocketClient } from "./ws.js";

const rtlStrongCharacter = /[\u0590-\u08ff\ufb1d-\ufdff\ufe70-\ufeff]/u;
const ltrStrongCharacter = /[A-Za-z\u00c0-\u02af\u0370-\u058f\u0900-\u1fff\u2c00-\u2dff\ua720-\ua7ff]/u;

// detectDirection follows the first strongly directional character rather
// than punctuation, whitespace, or numbers. The fallback keeps neutral text
// stable and matches the editor's LTR default.
export function detectDirection(text, fallback = "ltr") {
  for (const character of String(text ?? "")) {
    if (rtlStrongCharacter.test(character)) {
      return "rtl";
    }
    if (ltrStrongCharacter.test(character)) {
      return "ltr";
    }
  }
  return fallback === "rtl" ? "rtl" : "ltr";
}

export function createDirectionController(defaultDirection = "ltr") {
  let mode = "auto";
  let direction = defaultDirection === "rtl" ? "rtl" : "ltr";
  return {
    get direction() {
      return direction;
    },
    get mode() {
      return mode;
    },
    setManual(nextMode) {
      mode = ["auto", "ltr", "rtl"].includes(nextMode) ? nextMode : "auto";
      if (mode !== "auto") {
        direction = mode;
      }
      return direction;
    },
    update(text) {
      if (mode === "auto") {
        direction = detectDirection(text, "ltr");
      }
      return direction;
    },
  };
}

if (typeof document !== "undefined" && typeof document.querySelector === "function" && typeof window !== "undefined") {
const editor = document.querySelector("#editor");
const connectionStatus = document.querySelector("#connection-status");
const presenceStatus = document.querySelector("#presence-status");
const presenceList = document.querySelector("#presence-list");
const directionToggle = document.querySelector("#direction-toggle");
const remoteCursors = document.querySelector("#remote-cursors");
const slugMatch = window.location.pathname.match(/^\/d\/([0-9A-Za-z]{12})\/?$/);
const slug = slugMatch ? slugMatch[1] : "";
const documentState = new RgaDocument();
const presenceState = new PresenceState();
const usernameStorageKey = "gopad.username";

let siteID = "";
let nextCounter = 1;
let connected = false;
let ready = false;
let renderedText = "";
let cursorSendTimer = null;
const directionController = createDirectionController();

function loadUsername() {
  let username = "";
  try {
    username = window.localStorage.getItem(usernameStorageKey) || "";
  } catch {
    // Private browsing or disabled storage should not prevent editing.
  }
  if (!username && typeof window.prompt === "function") {
    try {
      username = window.prompt("Choose a display name", "Anonymous")?.trim() || "Anonymous";
    } catch {
      // Embedded browsers may disable JavaScript prompts; keep the editor usable.
      username = "Anonymous";
    }
    try {
      window.localStorage.setItem(usernameStorageKey, username);
    } catch {
      // The name remains usable for this session when storage is unavailable.
    }
  }
  return username || "Anonymous";
}

function setStatus(text, state) {
  connectionStatus.textContent = text;
  connectionStatus.dataset.state = state;
}

function applyDirection(text) {
  const direction = directionController.update(text);
  editor.dir = directionController.mode === "auto" ? "auto" : direction;
  editor.dataset.resolvedDirection = direction;
  if (directionToggle && directionToggle.value !== directionController.mode) {
    directionToggle.value = directionController.mode;
  }
}

function renderText() {
  const selectionStart = editor.selectionStart;
  const selectionEnd = editor.selectionEnd;
  const text = documentState.text();
  editor.value = text;
  renderedText = text;
  applyDirection(text);
  if (document.activeElement === editor) {
    const nextStart = Math.min(selectionStart, text.length);
    const nextEnd = Math.min(selectionEnd, text.length);
    editor.setSelectionRange(nextStart, nextEnd);
  }
  renderRemoteCursors(remoteCursors, editor, documentState, presenceState.cursorEntries());
}

function renderPresence() {
  const count = presenceState.collaboratorCount(siteID);
  presenceStatus.textContent = `${count} COLLABORATOR${count === 1 ? "" : "S"}`;
  if (presenceList) {
    presenceList.replaceChildren();
    for (const user of presenceState.userEntries()) {
      const item = document.createElement("span");
      item.className = "presence-user";
      item.title = user.username || "Collaborator";
      item.append(renderAvatar(user.username || "Collaborator", 18));
      const label = document.createElement("span");
      label.textContent = user.username || "Collaborator";
      item.append(label);
      presenceList.append(item);
    }
  }
  renderRemoteCursors(remoteCursors, editor, documentState, presenceState.cursorEntries());
}

function queueCursorUpdate() {
  if (cursorSendTimer !== null) {
    return;
  }
  cursorSendTimer = window.setTimeout(() => {
    cursorSendTimer = null;
    if (!ready || !connected || !siteID) {
      return;
    }
    const startOffset = Array.from(renderedText.slice(0, editor.selectionStart)).length;
    const endOffset = Array.from(renderedText.slice(0, editor.selectionEnd)).length;
    socket.send({
      type: "cursor",
      payload: {
        start: anchorForVisibleOffset(documentState, startOffset),
        end: anchorForVisibleOffset(documentState, endOffset),
      },
    });
  }, 50);
}

function handleEnvelope(envelope) {
  if (!envelope || typeof envelope.type !== "string") {
    return;
  }
  if (envelope.type === "sync") {
    const payload = envelope.payload || {};
    documentState.replaceSnapshot(payload.snapshot || []);
    siteID = payload.siteID || "";
    nextCounter = 1;
    presenceState.applySnapshot(payload.presence || []);
    renderText();
    renderPresence();
    ready = true;
    editor.disabled = !connected;
    setStatus(connected ? "Connected" : "Reconnecting…", connected ? "connected" : "reconnecting");
    return;
  }
  if (envelope.type === "op") {
    try {
      documentState.apply(envelope.payload);
      renderText();
    } catch (error) {
      setStatus(`Sync error: ${error.message}`, "error");
    }
    return;
  }
  if (envelope.type === "presence") {
    presenceState.applyPresence(envelope.payload || {});
    renderPresence();
    return;
  }
  if (envelope.type === "cursor") {
    presenceState.setCursor(envelope.payload || {});
    renderPresence();
    return;
  }
  if (envelope.type === "error") {
    setStatus(envelope.payload?.message || "The server rejected an operation", "error");
  }
}

editor.addEventListener("input", () => {
  if (!ready || !connected) {
    editor.value = renderedText;
    return;
  }
  const { operations, nextCounter: updatedCounter } = createTextOperations(
    documentState,
    renderedText,
    editor.value,
    { siteID, counter: nextCounter },
  );
  nextCounter = updatedCounter;
  for (const operation of operations) {
    documentState.apply(operation);
    socket.send({ type: "op", payload: encodeOperation(operation) });
  }
  renderedText = documentState.text();
  applyDirection(renderedText);
  queueCursorUpdate();
});

editor.addEventListener("select", queueCursorUpdate);
editor.addEventListener("click", queueCursorUpdate);
editor.addEventListener("keyup", queueCursorUpdate);
editor.addEventListener("scroll", renderPresence);
directionToggle?.addEventListener("change", () => {
  directionController.setManual(directionToggle.value);
  applyDirection(editor.value);
});

const username = loadUsername();
void username;

const websocketProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";
const socket = new SocketClient(`${websocketProtocol}//${window.location.host}/ws/${slug}`, {
  onStatus: (state) => {
    if (state === "connecting") {
      setStatus("Connecting…", "connecting");
    } else if (state === "reconnecting") {
      connected = false;
      ready = false;
      editor.disabled = true;
      setStatus("Reconnecting…", "reconnecting");
    }
  },
  onOpen: () => {
    connected = true;
    ready = false;
    editor.disabled = true;
    socket.send({ type: "hello", payload: { username } });
    setStatus("Connected; syncing…", "connecting");
  },
  onMessage: handleEnvelope,
  onError: () => setStatus("Connection error", "error"),
  onClose: () => {
    connected = false;
    ready = false;
    editor.disabled = true;
    presenceState.applySnapshot([]);
    renderPresence();
    setStatus("Disconnected", "reconnecting");
  },
});

socket.connect();
}
