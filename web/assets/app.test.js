import assert from "node:assert/strict";
import test from "node:test";

import { createDirectionController, detectDirection } from "./app.js";

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
