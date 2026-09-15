import assert from "node:assert/strict";
import test from "node:test";

import { clampSequence, formatSequence, nextSequence } from "./history.js";

test("clampSequence keeps a scrubber position inside fetched bounds", () => {
  assert.equal(clampSequence(-5, 0, 10), 0);
  assert.equal(clampSequence(4, 0, 10), 4);
  assert.equal(clampSequence(99, 0, 10), 10);
});
test("nextSequence steps in either direction without crossing archive edges", () => {
  assert.equal(nextSequence(5, -1, 0, 10), 4);
  assert.equal(nextSequence(0, -1, 0, 10), 0);
  assert.equal(nextSequence(10, 1, 0, 10), 10);
});

test("formatSequence produces stable terminal readouts", () => {
  assert.equal(formatSequence(7), "000007");
  assert.equal(formatSequence(123, 4), "0123");
  assert.equal(formatSequence(-1), "000000");
});
