import assert from "node:assert/strict";
import test from "node:test";

import { renderAvatar } from "./avatar.js";

class FakeElement {
  constructor(tagName) {
    this.tagName = tagName;
    this.attributes = new Map();
    this.children = [];
    this.textContent = "";
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  append(...children) {
    this.children.push(...children);
  }

  get childElementCount() {
    return this.children.length;
  }

  get outerHTML() {
    const attributes = [...this.attributes.entries()]
      .map(([name, value]) => ` ${name}="${escapeHTML(value)}"`)
      .join("");
    const content = this.textContent + this.children.map((child) => child.outerHTML).join("");
    return `<${this.tagName}${attributes}>${content}</${this.tagName}>`;
  }
}

function escapeHTML(value) {
  return value.replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;");
}

globalThis.document = {
  createElementNS(_namespace, tagName) {
    return new FakeElement(tagName);
  },
};

function countNodes(element) {
  return 1 + element.children.reduce((total, child) => total + countNodes(child), 0);
}

test("renderAvatar is deterministic for a username", () => {
  const first = renderAvatar("Alice", 24).outerHTML;
  const second = renderAvatar("Alice", 24).outerHTML;
  assert.equal(first, second);
});

test("different usernames produce distinct compact SVG avatars", () => {
  const usernames = ["Alice", "Bob", "Carol", "Dave", "Eve", "Farid", "Grace", "Hamed"];
  const outputs = new Set(usernames.map((username) => renderAvatar(username, 24).outerHTML));
  assert.equal(outputs.size, usernames.length);

  const avatar = renderAvatar("Farid", 32);
  assert.equal(avatar.tagName, "svg");
  assert.equal(avatar.attributes.get("viewBox"), "0 0 5 5");
  assert.ok(countNodes(avatar) <= 30);
  assert.doesNotMatch(avatar.outerHTML, /<image|<use|url\(/i);
});
