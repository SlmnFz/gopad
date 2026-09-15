import { cloneCharID, sameCharID } from "./crdt.js";

export function anchorForVisibleOffset(documentState, offset) {
  const visible = documentState.visibleCharacters();
  const boundedOffset = Math.max(0, Math.min(Number(offset) || 0, visible.length));
  return {
    leftID: boundedOffset > 0 ? cloneCharID(visible[boundedOffset - 1].id) : null,
    rightID: boundedOffset < visible.length ? cloneCharID(visible[boundedOffset].id) : null,
  };
}

export function visibleOffsetForAnchor(documentState, anchor = {}) {
  const visible = documentState.visibleCharacters();
  const snapshot = documentState.snapshot();
  const visibleIndex = (id) => visible.findIndex((char) => sameCharID(char.id, id));
  const structuralIndex = (id) => snapshot.findIndex((char) => sameCharID(char.id, id));

  const directRight = visibleIndex(anchor.rightID);
  if (directRight >= 0) {
    return directRight;
  }
  const rightPosition = structuralIndex(anchor.rightID);
  if (rightPosition >= 0) {
    for (let index = rightPosition; index < snapshot.length; index += 1) {
      if (!snapshot[index].deleted) {
        return visibleIndex(snapshot[index].id);
      }
    }
  }

  const directLeft = visibleIndex(anchor.leftID);
  if (directLeft >= 0) {
    return directLeft + 1;
  }
  const leftPosition = structuralIndex(anchor.leftID);
  if (leftPosition >= 0) {
    for (let index = leftPosition; index >= 0; index -= 1) {
      if (!snapshot[index].deleted) {
        return visibleIndex(snapshot[index].id) + 1;
      }
    }
  }
  return 0;
}

export class PresenceState {
  constructor() {
    this.users = new Map();
    this.cursors = new Map();
  }

  applySnapshot(users = []) {
    this.users.clear();
    this.cursors.clear();
    for (const user of users) {
      if (user.siteID) {
        this.users.set(user.siteID, user);
      }
    }
  }

  applyPresence(message) {
    if (Array.isArray(message.active)) {
      this.users.clear();
      const activeSites = new Set();
      for (const user of message.active) {
        if (user.siteID) {
          this.users.set(user.siteID, user);
          activeSites.add(user.siteID);
        }
      }
      for (const siteID of this.cursors.keys()) {
        if (!activeSites.has(siteID)) {
          this.cursors.delete(siteID);
        }
      }
    } else if (message.event === "join" && message.user?.siteID) {
      this.users.set(message.user.siteID, message.user);
    } else if (message.event === "leave" && message.user?.siteID) {
      this.users.delete(message.user.siteID);
      this.cursors.delete(message.user.siteID);
    }
  }

  setCursor(cursor) {
    if (cursor?.siteID) {
      this.cursors.set(cursor.siteID, cursor);
      this.users.set(cursor.siteID, cursor);
    }
  }

  removeSite(siteID) {
    this.users.delete(siteID);
    this.cursors.delete(siteID);
  }

  collaboratorCount(ownSiteID = "") {
    return [...this.users.keys()].filter((siteID) => siteID !== ownSiteID).length;
  }

  cursorEntries() {
    return [...this.cursors.values()];
  }
}

export function renderRemoteCursors(container, textarea, documentState, cursors) {
  container.replaceChildren();
  const computed = getComputedStyle(textarea);
  const fontSize = Number.parseFloat(computed.fontSize) || 16;
  const lineHeight = Number.parseFloat(computed.lineHeight) || fontSize * 1.8;
  const paddingLeft = Number.parseFloat(computed.paddingLeft) || 0;
  const paddingTop = Number.parseFloat(computed.paddingTop) || 0;
  const characterWidth = fontSize * 0.6;
  const text = documentState.text();

  for (const cursor of cursors) {
    const offset = visibleOffsetForAnchor(documentState, cursor.start);
    const before = text.slice(0, offset);
    const lines = before.split("\n");
    const column = Array.from(lines.at(-1) || "").length;
    const marker = document.createElement("span");
    marker.className = "remote-cursor";
    marker.style.setProperty("--cursor-color", cursor.color || "#4dd8c0");
    marker.style.left = `${paddingLeft + column * characterWidth - textarea.scrollLeft}px`;
    marker.style.top = `${paddingTop + (lines.length - 1) * lineHeight - textarea.scrollTop}px`;
    marker.title = cursor.username || "Collaborator";

    const label = document.createElement("span");
    label.className = "remote-cursor-label";
    label.textContent = cursor.username || "Collaborator";
    marker.append(label);
    container.append(marker);
  }
}
