import assert from "node:assert/strict";
import test from "node:test";

import { decryptFrame } from "./decrypt.js";

test("decryptFrame reveals characters progressively and preserves whitespace", () => {
  const random = () => 0;
  assert.equal(decryptFrame("AB CD", 0, "X", random), "XX XX");
  assert.equal(decryptFrame("AB CD", 2, "X", random), "AB XX");
  assert.equal(decryptFrame("AB CD", 99, "X", random), "AB CD");
});
