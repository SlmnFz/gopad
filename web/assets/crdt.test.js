import assert from "node:assert/strict";
import test from "node:test";

import {
  RgaDocument,
  createTextOperations,
  encodeOperation,
} from "./crdt.js";

test("concurrent sibling inserts converge on the same visible text", () => {
  const a = { type: "insert", id: { siteID: "a", counter: 1 }, value: "A", leftID: null };
  const b = { type: "insert", id: { siteID: "b", counter: 1 }, value: "B", leftID: null };
  const first = new RgaDocument();
  const second = new RgaDocument();

  first.apply(a);
  first.apply(b);
  second.apply(b);
  second.apply(a);

  assert.equal(first.text(), "AB");
  assert.equal(second.text(), "AB");
  assert.deepEqual(first.snapshot(), second.snapshot());
});

test("deletes preserve tombstones while removing characters from visible text", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });

  document.apply({ type: "delete", id: firstID });

  assert.equal(document.text(), "B");
  assert.equal(document.snapshot()[0].deleted, true);
  assert.equal(document.snapshot().length, 2);
});

test("visible offsets map inserts to the previous visible character", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });

  assert.deepEqual(document.leftIDForVisibleOffset(0), null);
  assert.deepEqual(document.leftIDForVisibleOffset(1), firstID);
  assert.deepEqual(document.leftIDForVisibleOffset(2), secondID);
});

test("textarea replacement creates a delete and ordered inserts", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });

  const { operations, nextCounter } = createTextOperations(document, "AB", "AXY", {
    siteID: "editor",
    counter: 10,
  });
  assert.deepEqual(operations[0], { type: "delete", id: secondID });
  assert.deepEqual(operations[1], { type: "insert", id: { siteID: "editor", counter: 10 }, value: "X", leftID: firstID });
  assert.deepEqual(operations[2], {
    type: "insert",
    id: { siteID: "editor", counter: 11 },
    value: "Y",
    leftID: { siteID: "editor", counter: 10 },
  });
  assert.equal(nextCounter, 12);

  for (const operation of operations) {
    document.apply(operation);
  }
  assert.equal(document.text(), "AXY");
});

test("textarea deletion targets the CRDT characters at the visible range", () => {
  const document = new RgaDocument();
  const firstID = { siteID: "site", counter: 1 };
  const secondID = { siteID: "site", counter: 2 };
  const thirdID = { siteID: "site", counter: 3 };
  document.apply({ type: "insert", id: firstID, value: "A", leftID: null });
  document.apply({ type: "insert", id: secondID, value: "B", leftID: firstID });
  document.apply({ type: "insert", id: thirdID, value: "C", leftID: secondID });

  const { operations } = createTextOperations(document, "ABC", "AC", {
    siteID: "editor",
  });
  assert.deepEqual(operations, [{ type: "delete", id: secondID }]);
  document.apply(operations[0]);
  assert.equal(document.text(), "AC");
});

test("wire operations use numeric Unicode code points like the Go protocol", () => {
  const encoded = encodeOperation({
    type: "insert",
    id: { siteID: "editor", counter: 1 },
    value: "你",
    leftID: null,
  });
  assert.equal(encoded.value, "你".codePointAt(0));
  assert.equal("你".length, 1);
});
