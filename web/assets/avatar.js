const SVG_NAMESPACE = "http://www.w3.org/2000/svg";

// fnv1a32 is deliberately small and stable across browsers and reconnects.
export function fnv1a32(value) {
  let hash = 0x811c9dc5;
  for (const character of String(value ?? "")) {
    const codePoint = character.codePointAt(0);
    hash ^= codePoint & 0xff;
    hash = Math.imul(hash, 0x01000193);
    if (codePoint > 0xff) {
      hash ^= codePoint >>> 8;
      hash = Math.imul(hash, 0x01000193);
    }
  }
  return hash >>> 0;
}

function mulberry32(seed) {
  return () => {
    let value = (seed += 0x6d2b79f5);
    value = Math.imul(value ^ (value >>> 15), value | 1);
    value ^= value + Math.imul(value ^ (value >>> 7), value | 61);
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

function palette(seed, random) {
  const hue = 135 + Math.floor((seed % 180) * 0.8 + random() * 28);
  return {
    background: `hsl(${hue} 34% 12%)`,
    foreground: `hsl(${hue} 78% 68%)`,
  };
}

function appendAttribute(element, name, value) {
  element.setAttribute(name, String(value));
  return element;
}

// renderAvatar creates a compact mirrored 5×5 SVG identicon. It uses only
// local DOM primitives and no external references or image resources.
export function renderAvatar(username, size = 24) {
  if (typeof document === "undefined") {
    throw new Error("renderAvatar requires a browser document");
  }
  const safeName = String(username ?? "Anonymous").trim() || "Anonymous";
  const safeSize = Math.max(12, Number(size) || 24);
  const seed = fnv1a32(safeName);
  const random = mulberry32(seed);
  const colors = palette(seed, random);
  const svg = document.createElementNS(SVG_NAMESPACE, "svg");
  appendAttribute(svg, "class", "user-avatar");
  appendAttribute(svg, "width", safeSize);
  appendAttribute(svg, "height", safeSize);
  appendAttribute(svg, "viewBox", "0 0 5 5");
  appendAttribute(svg, "role", "img");
  appendAttribute(svg, "aria-label", `${safeName} avatar`);
  appendAttribute(svg, "data-avatar-seed", seed);

  const title = document.createElementNS(SVG_NAMESPACE, "title");
  title.textContent = `${safeName} avatar`;
  svg.append(title);

  const background = document.createElementNS(SVG_NAMESPACE, "rect");
  appendAttribute(background, "width", 5);
  appendAttribute(background, "height", 5);
  appendAttribute(background, "fill", colors.background);
  svg.append(background);

  const cells = [];
  for (let row = 0; row < 5; row += 1) {
    for (let column = 0; column < 3; column += 1) {
      const filled = column === 2 ? random() > 0.34 : random() > 0.48;
      if (!filled) {
        continue;
      }
      cells.push([row, column]);
      if (column < 2) {
        cells.push([row, 4 - column]);
      }
    }
  }

  for (const [row, column] of cells) {
    const cell = document.createElementNS(SVG_NAMESPACE, "rect");
    appendAttribute(cell, "x", column);
    appendAttribute(cell, "y", row);
    appendAttribute(cell, "width", 1);
    appendAttribute(cell, "height", 1);
    appendAttribute(cell, "fill", colors.foreground);
    svg.append(cell);
  }
  return svg;
}
