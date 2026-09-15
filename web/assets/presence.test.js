import assert from "node:assert/strict";
import test from "node:test";

import { RgaDocument } from "./crdt.js";
import {
  anchorForVisibleOffset,
  PresenceState,
  remoteCursorInlinePosition,
  visibleOffsetForAnchor,
} from "./presence.js";

test("cursor anchors follow the adjacent character after a concurrent insert", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });

  const anchor = anchorForVisibleOffset(document, 1);
  document.apply({
    type: "insert",
    id: { siteID: "aaaa", counter: 1 },
    value: "X",
    leftID: firstID,
  });

  assert.deepEqual(anchor.leftID, firstID);
  assert.deepEqual(anchor.rightID, secondID);
  assert.equal(visibleOffsetForAnchor(document, anchor), 2);
});

test("presence snapshots and leave events remove users and cursors", () => {
  const presence = new PresenceState();
  presence.applySnapshot([
    { siteID: "alice-tab", userID: 1, username: "Alice", color: "#4dd8c0" },
    { siteID: "bob-tab", userID: 2, username: "Bob", color: "#7aa2f7" },
  ]);
  presence.setCursor({ siteID: "bob-tab", username: "Bob", start: {}, end: {} });

  assert.equal(presence.collaboratorCount("alice-tab"), 1);
  presence.applyPresence({
    event: "leave",
    user: { siteID: "bob-tab" },
    active: [{ siteID: "alice-tab", userID: 1, username: "Alice", color: "#4dd8c0" }],
  });
  assert.equal(presence.collaboratorCount("alice-tab"), 0);
  assert.deepEqual(presence.cursorEntries(), []);
});

test("remote cursor positions mirror the editor direction", () => {
  assert.deepEqual(remoteCursorInlinePosition("ltr", 3, {
    paddingLeft: 8,
    characterWidth: 10,
  }), { side: "left", offset: 38 });
  assert.deepEqual(remoteCursorInlinePosition("rtl", 3, {
    paddingRight: 12,
    characterWidth: 10,
  }), { side: "right", offset: 42 });
});
