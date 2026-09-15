const DEFAULT_CHARACTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZ#$%&01";

// decryptFrame is pure so the visual effect can be tested without a browser.
export function decryptFrame(text, revealCount, characters = DEFAULT_CHARACTERS, random = Math.random) {
  const source = Array.from(String(text ?? ""));
  const count = Math.max(0, Math.min(Number(revealCount) || 0, source.length));
  const pool = String(characters || DEFAULT_CHARACTERS);
  return source
    .map((character, index) => {
      if (index < count || /\s/u.test(character)) {
        return character;
      }
      return pool[Math.floor(Math.max(0, Math.min(0.999999, random())) * pool.length)] || character;
    })
    .join("");
}

export function prefersReducedMotion() {
  return typeof window !== "undefined"
    && typeof window.matchMedia === "function"
    && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

// decryptReveal applies the one-shot terminal scramble used by the brand and
// by history checkpoints. It never injects HTML, so document text remains
// plain text even while it is being revealed.
export function decryptReveal(element, finalText, options = {}) {
  if (!element) {
    return Promise.resolve();
  }
  if (typeof element.__gopadCancelDecrypt === "function") {
    element.__gopadCancelDecrypt();
  }

  const text = String(finalText ?? "");
  element.classList.add("decrypt-target");
  if (prefersReducedMotion() || typeof window === "undefined") {
    element.textContent = text;
    element.removeAttribute("data-decrypting");
    return Promise.resolve();
  }

  const totalFrames = Math.max(1, Number(options.frames) || 18);
  const duration = Math.max(80, Number(options.duration) || 520);
  const characters = options.characters || DEFAULT_CHARACTERS;
  const interval = Math.max(16, Math.round(duration / totalFrames));
  let frame = 0;
  let timer = null;
  let resolveAnimation;
  const promise = new Promise((resolve) => {
    resolveAnimation = resolve;
  });

  const finish = () => {
    if (timer !== null) {
      window.clearInterval(timer);
      timer = null;
    }
    element.textContent = text;
    element.removeAttribute("data-decrypting");
    if (element.__gopadCancelDecrypt === cancel) {
      delete element.__gopadCancelDecrypt;
    }
    resolveAnimation();
  };
  const cancel = () => finish();
  element.__gopadCancelDecrypt = cancel;
  element.dataset.decrypting = "true";
  element.textContent = decryptFrame(text, 0, characters);
  timer = window.setInterval(() => {
    frame += 1;
    element.textContent = decryptFrame(text, Math.floor((frame / totalFrames) * Array.from(text).length), characters);
    if (frame >= totalFrames) {
      finish();
    }
  }, interval);
  return promise;
}
