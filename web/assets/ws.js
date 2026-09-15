const BACKOFF_MS = [250, 500, 1000, 2000, 4000, 8000];

export class SocketClient {
  constructor(url, handlers = {}) {
    this.url = url;
    this.handlers = handlers;
    this.socket = null;
    this.reconnectTimer = null;
    this.attempt = 0;
    this.closed = true;
  }

  connect() {
    if (!this.closed || this.socket) {
      return;
    }
    this.closed = false;
    this.open();
  }

  open() {
    this.handlers.onStatus?.("connecting");
    const socket = new WebSocket(this.url);
    this.socket = socket;
    socket.addEventListener("open", () => {
      if (this.socket !== socket) {
        return;
      }
      this.attempt = 0;
      this.handlers.onOpen?.();
    });
    socket.addEventListener("message", (event) => {
      if (this.socket !== socket) {
        return;
      }
      try {
        this.handlers.onMessage?.(JSON.parse(event.data));
      } catch (error) {
        this.handlers.onError?.(error);
      }
    });
    socket.addEventListener("error", (event) => {
      if (this.socket === socket) {
        this.handlers.onError?.(event);
      }
    });
    socket.addEventListener("close", (event) => {
      if (this.socket !== socket) {
        return;
      }
      this.socket = null;
      this.handlers.onClose?.(event);
      if (!this.closed) {
        this.scheduleReconnect();
      }
    });
  }

  scheduleReconnect() {
    if (this.reconnectTimer || this.closed) {
      return;
    }
    const delay = BACKOFF_MS[Math.min(this.attempt, BACKOFF_MS.length - 1)];
    this.attempt += 1;
    this.handlers.onStatus?.("reconnecting", delay);
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  send(message) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) {
      return false;
    }
    this.socket.send(typeof message === "string" ? message : JSON.stringify(message));
    return true;
  }

  close() {
    this.closed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.socket) {
      this.socket.close(1000, "client closed");
      this.socket = null;
    }
  }
}

export { BACKOFF_MS };
