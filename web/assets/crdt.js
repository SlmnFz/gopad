const ROOT_KEY = "";

export function cloneCharID(id) {
  return id ? { siteID: id.siteID, counter: id.counter } : null;
}

export function sameCharID(left, right) {
  if (left === null || left === undefined || right === null || right === undefined) {
    return (left === null || left === undefined) && (right === null || right === undefined);
  }
  return left.siteID === right.siteID && left.counter === right.counter;
}

export function compareCharID(left, right) {
  if (left.counter !== right.counter) {
    return left.counter - right.counter;
  }
  if (left.siteID < right.siteID) {
    return -1;
  }
  if (left.siteID > right.siteID) {
    return 1;
  }
  return 0;
}

function charKey(id) {
  return `${id.siteID}:${id.counter}`;
}

function normalizeValue(value) {
  if (typeof value === "number") {
    if (!Number.isInteger(value) || value < 0 || value > 0x10ffff) {
      throw new Error("character value must be a Unicode code point");
    }
    return String.fromCodePoint(value);
  }
  if (typeof value === "string" && Array.from(value).length === 1) {
    return value;
  }
  throw new Error("character value must contain one Unicode character");
}

function validateID(id) {
  if (!id || typeof id.siteID !== "string" || id.siteID.length === 0 || !Number.isInteger(id.counter) || id.counter <= 0) {
    throw new Error("invalid character ID");
  }
}

function cloneChar(char) {
  return {
    id: cloneCharID(char.id),
    value: char.value,
    leftID: cloneCharID(char.leftID),
    rightID: cloneCharID(char.rightID),
    deleted: char.deleted,
  };
}

export class RgaDocument {
  constructor(snapshot = []) {
    this.characters = new Map();
    this.replaceSnapshot(snapshot);
  }

  apply(operation) {
    if (!operation || (operation.type !== "insert" && operation.type !== "delete")) {
      throw new Error("invalid operation type");
    }
    validateID(operation.id);

    if (operation.type === "delete") {
      const existing = this.characters.get(charKey(operation.id));
      if (!existing) {
        throw new Error("unknown character");
      }
      if (existing.deleted) {
        return false;
      }
      existing.deleted = true;
      return true;
    }

    const id = cloneCharID(operation.id);
    const leftID = cloneCharID(operation.leftID);
    const rightID = cloneCharID(operation.rightID);
    const value = normalizeValue(operation.value);
    const key = charKey(id);
    const existing = this.characters.get(key);
    if (existing) {
      if (existing.value === value && sameCharID(existing.leftID, leftID) && sameCharID(existing.rightID, rightID)) {
        return false;
      }
      throw new Error("conflicting duplicate character");
    }
    if (leftID !== null) {
      validateID(leftID);
      if (sameCharID(id, leftID) || !this.characters.has(charKey(leftID))) {
        throw new Error("missing parent character");
      }
    }
    if (rightID !== null && (sameCharID(id, rightID) || sameCharID(leftID, rightID))) {
      throw new Error("invalid right anchor");
    }
    this.characters.set(key, { id, value, leftID, rightID, deleted: false });
    return true;
  }

  replaceSnapshot(snapshot) {
    this.characters.clear();
    for (const char of snapshot || []) {
      this.apply({
        type: "insert",
        id: char.id,
        value: char.value,
        leftID: char.leftID ?? null,
        rightID: char.rightID ?? null,
      });
      if (char.deleted) {
        this.apply({ type: "delete", id: char.id });
      }
    }
  }

  snapshot() {
    const children = new Map();
    for (const char of this.characters.values()) {
      const parentKey = char.leftID ? charKey(char.leftID) : ROOT_KEY;
      const siblings = children.get(parentKey) || [];
      siblings.push(char);
      children.set(parentKey, siblings);
    }
    for (const siblings of children.values()) {
      siblings.sort((left, right) => compareCharID(left.id, right.id));
    }

    const base = [];
    const visit = (parentKey) => {
      for (const char of children.get(parentKey) || []) {
        base.push(char);
        visit(charKey(char.id));
      }
    };
    visit(ROOT_KEY);

    const baseIndex = new Map(base.map((char, index) => [charKey(char.id), index]));
    const indegree = new Map(base.map((char) => [charKey(char.id), 0]));
    const edges = new Map(base.map((char) => [charKey(char.id), []]));
    const addEdge = (fromID, toID) => {
      if (!fromID || !toID || sameCharID(fromID, toID)) {
        return;
      }
      const fromKey = charKey(fromID);
      const toKey = charKey(toID);
      if (!indegree.has(fromKey) || !indegree.has(toKey)) {
        return;
      }
      const outgoing = edges.get(fromKey);
      if (outgoing.includes(toKey)) {
        return;
      }
      outgoing.push(toKey);
      indegree.set(toKey, indegree.get(toKey) + 1);
    };
    for (const char of base) {
      addEdge(char.leftID, char.id);
      addEdge(char.id, char.rightID);
    }

    const ready = [];
    const compareReady = (left, right) => baseIndex.get(left) - baseIndex.get(right);
    const pushReady = (key) => {
      let index = ready.length;
      ready.push(key);
      while (index > 0) {
        const parent = Math.floor((index - 1) / 2);
        if (compareReady(ready[parent], key) <= 0) {
          break;
        }
        ready[index] = ready[parent];
        index = parent;
      }
      ready[index] = key;
    };
    const popReady = () => {
      const first = ready[0];
      const last = ready.pop();
      if (ready.length > 0) {
        let index = 0;
        while (true) {
          const left = index * 2 + 1;
          if (left >= ready.length) {
            break;
          }
          const right = left + 1;
          let child = left;
          if (right < ready.length && compareReady(ready[right], ready[left]) < 0) {
            child = right;
          }
          if (compareReady(last, ready[child]) <= 0) {
            break;
          }
          ready[index] = ready[child];
          index = child;
        }
        ready[index] = last;
      }
      return first;
    };

    for (const char of base) {
      if (indegree.get(charKey(char.id)) === 0) {
        pushReady(charKey(char.id));
      }
    }
    const ordered = [];
    const seen = new Set();
    while (ready.length > 0) {
      const key = popReady();
      seen.add(key);
      ordered.push(cloneChar(this.characters.get(key)));
      for (const child of edges.get(key)) {
        const nextDegree = indegree.get(child) - 1;
        indegree.set(child, nextDegree);
        if (nextDegree === 0) {
          pushReady(child);
        }
      }
    }

    // Keep the deterministic legacy order for malformed/cyclic anchors.
    for (const char of base) {
      const key = charKey(char.id);
      if (!seen.has(key)) {
        ordered.push(cloneChar(char));
      }
    }
    return ordered;
  }

  visibleCharacters() {
    return this.snapshot().filter((char) => !char.deleted);
  }

  text() {
    return this.visibleCharacters().map((char) => char.value).join("");
  }

  leftIDForVisibleOffset(offset) {
    const visible = this.visibleCharacters();
    const boundedOffset = Math.max(0, Math.min(Number(offset) || 0, visible.length));
    return boundedOffset === 0 ? null : cloneCharID(visible[boundedOffset - 1].id);
  }

  rightIDForVisibleOffset(offset) {
    const visible = this.visibleCharacters();
    const boundedOffset = Math.max(0, Math.min(Number(offset) || 0, visible.length));
    return boundedOffset === visible.length ? null : cloneCharID(visible[boundedOffset].id);
  }
}

// Convert one textarea edit into a contiguous delete/insert operation batch.
// The editor intentionally disables input while disconnected, so this covers
// the local, single-user edit between two authoritative visible states.
export function createTextOperations(document, previousText, nextText, { siteID, counter = 1 } = {}) {
  if (!(document instanceof RgaDocument)) {
    throw new Error("document is required");
  }
  if (typeof siteID !== "string" || siteID.length === 0) {
    throw new Error("siteID is required");
  }
  const previous = Array.from(previousText);
  const next = Array.from(nextText);
  const visible = document.visibleCharacters();
  if (visible.length !== previous.length || document.text() !== previousText) {
    throw new Error("previous text does not match the document");
  }

  let prefix = 0;
  while (prefix < previous.length && prefix < next.length && previous[prefix] === next[prefix]) {
    prefix += 1;
  }
  let previousEnd = previous.length;
  let nextEnd = next.length;
  while (previousEnd > prefix && nextEnd > prefix && previous[previousEnd - 1] === next[nextEnd - 1]) {
    previousEnd -= 1;
    nextEnd -= 1;
  }

  const operations = [];
  for (const char of visible.slice(prefix, previousEnd)) {
    operations.push({ type: "delete", id: cloneCharID(char.id) });
  }

  let nextCounter = counter;
  let leftID = document.leftIDForVisibleOffset(prefix);
  const rightID = document.rightIDForVisibleOffset(prefix);
  for (const value of next.slice(prefix, nextEnd)) {
    const id = { siteID, counter: nextCounter };
    nextCounter += 1;
    operations.push({ type: "insert", id, value, leftID: cloneCharID(leftID), rightID: cloneCharID(rightID) });
    leftID = id;
  }

  return { operations, nextCounter };
}

export function encodeOperation(operation) {
  const wireOperation = {
    type: operation.type,
    id: cloneCharID(operation.id),
  };
  if (operation.leftID) {
    wireOperation.leftID = cloneCharID(operation.leftID);
  }
  if (operation.rightID) {
    wireOperation.rightID = cloneCharID(operation.rightID);
  }
  if (operation.type === "insert") {
    wireOperation.value = operation.value.codePointAt(0);
  }
  return wireOperation;
}
