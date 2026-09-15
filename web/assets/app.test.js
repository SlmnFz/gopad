import assert from "node:assert/strict";
import test from "node:test";

import { RgaDocument } from "./crdt.js";
import {
  createDirectionController,
  detectDirection,
  editorOffsetToVisibleOffset,
  selectionAnchors,
  selectionOffsets,
  visibleOffsetToEditorOffset,
} from "./app.js";

test("detectDirection follows the first strong directional character", () => {
  assert.equal(detectDirection("سلام به Gopad"), "rtl");
  assert.equal(detectDirection("Gopad ویرایشگر"), "ltr");
  assert.equal(detectDirection("123 — ..."), "ltr");
});

test("manual direction override is not replaced by auto-detection", () => {
  const direction = createDirectionController();
  assert.equal(direction.update("سلام"), "rtl");
  direction.setManual("ltr");
  assert.equal(direction.update("سلام"), "ltr");
  direction.setManual("auto");
  assert.equal(direction.update("سلام"), "rtl");
});

test("editor selections follow remote inserts by character identity", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });

  const selection = selectionAnchors(document, "AB", 1, 1);
  document.apply({
    type: "insert",
    id: { siteID: "remote", counter: 1 },
    value: "X",
    leftID: firstID,
  });

  assert.deepEqual(selectionOffsets(document, document.text(), selection), { start: 2, end: 2 });
});

test("editor offsets handle Unicode code points", () => {
  const text = "A😀B";
  assert.equal(editorOffsetToVisibleOffset(text, 1), 1);
  assert.equal(editorOffsetToVisibleOffset(text, 3), 2);
  assert.equal(visibleOffsetToEditorOffset(text, 2), 3);
});
