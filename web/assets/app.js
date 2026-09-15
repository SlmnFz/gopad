import { RgaDocument, createTextOperations, encodeOperation } from "./crdt.js";
import { SocketClient } from "./ws.js";

const editor = document.querySelector("#editor");
const connectionStatus = document.querySelector("#connection-status");
const slugMatch = window.location.pathname.match(/^\/d\/([0-9A-Za-z]{12})\/?$/);
const slug = slugMatch ? slugMatch[1] : "";
const documentState = new RgaDocument();
const usernameStorageKey = "gopad.username";

let siteID = "";
let nextCounter = 1;
let connected = false;
let ready = false;
let renderedText = "";

function loadUsername() {
  let username = "";
  try {
    username = window.localStorage.getItem(usernameStorageKey) || "";
  } catch {
    // Private browsing or disabled storage should not prevent editing.
  }
  if (!username && typeof window.prompt === "function") {
    username = window.prompt("Choose a display name", "Anonymous")?.trim() || "Anonymous";
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

function renderText() {
  const selectionStart = editor.selectionStart;
  const selectionEnd = editor.selectionEnd;
  const text = documentState.text();
  editor.value = text;
  renderedText = text;
  if (document.activeElement === editor) {
    const nextStart = Math.min(selectionStart, text.length);
    const nextEnd = Math.min(selectionEnd, text.length);
    editor.setSelectionRange(nextStart, nextEnd);
  }
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
    renderText();
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
    setStatus("Connected; syncing…", "connecting");
  },
  onMessage: handleEnvelope,
  onError: () => setStatus("Connection error", "error"),
  onClose: () => {
    connected = false;
    ready = false;
    editor.disabled = true;
    setStatus("Disconnected", "reconnecting");
  },
});

socket.connect();
