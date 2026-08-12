import { Worker } from "node:worker_threads";

const START_TIMEOUT_MS = 15_000;
const SIGN_TIMEOUT_MS = 8_000;

export class WorkerEncryptor {
  constructor(graph, targetURL, options = {}) {
    this.graph = serializeGraph(graph);
    this.targetURL = targetURL;
    this.startTimeoutMs = options.startTimeoutMs || START_TIMEOUT_MS;
    this.signTimeoutMs = options.signTimeoutMs || SIGN_TIMEOUT_MS;
    this.deviceId = options.deviceId || "";
    this.assetDir = options.assetDir || "";
    this.api = null;
    this.worker = null;
    this.starting = null;
    this.terminating = null;
    this.pending = new Map();
    this.nextID = 0;
    this.closed = false;
  }

  async init() {
    await this.ensureWorker();
  }

  async encrypt(value, timestamp) {
    const worker = await this.ensureWorker();
    const id = String(++this.nextID);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        const error = timeoutError("signer export exceeded execution deadline");
        reject(error);
        void this.terminate(error);
      }, this.signTimeoutMs);
      timer.unref?.();
      this.pending.set(id, { resolve, reject, timer });
      worker.postMessage({ type: "encrypt", id, value, timestamp, deviceId: this.deviceId });
    });
  }

  setDeviceId(deviceId) {
    this.deviceId = String(deviceId || "");
  }

  async close() {
    this.closed = true;
    await this.terminate(new Error("signer executor closed"));
  }

  async ensureWorker() {
    if (this.closed) throw new Error("signer executor is closed");
    if (this.terminating) await this.terminating;
    if (this.closed) throw new Error("signer executor is closed");
    if (this.worker) return this.worker;
    if (this.starting) return this.starting;
    this.starting = this.spawn();
    try {
      return await this.starting;
    } finally {
      this.starting = null;
    }
  }

  spawn() {
    return new Promise((resolve, reject) => {
      const worker = new Worker(new URL("./oklink_signer_worker.mjs", import.meta.url), {
        workerData: { graph: this.graph, targetURL: this.targetURL, deviceId: this.deviceId, assetDir: this.assetDir },
      });
      const timer = setTimeout(() => {
        const error = timeoutError("signer initialization exceeded execution deadline");
        void worker.terminate();
        reject(error);
      }, this.startTimeoutMs);
      timer.unref?.();
      const fail = (error) => {
        clearTimeout(timer);
        if (this.worker === worker) this.worker = null;
        this.rejectPending(error);
        reject(error);
      };
      worker.once("error", fail);
      worker.once("exit", (code) => {
        if (code !== 0 && this.worker === worker) fail(new Error("signer worker exited unexpectedly"));
      });
      worker.on("message", (message) => {
        if (message?.type === "ready") {
          clearTimeout(timer);
          this.worker = worker;
          this.api = typeof message.api === "string" ? message.api : "legacy";
          resolve(worker);
          return;
        }
        if (message?.type !== "result") return;
        const pending = this.pending.get(message.id);
        if (!pending) return;
        this.pending.delete(message.id);
        clearTimeout(pending.timer);
        if (message.ok) pending.resolve(message.value);
        else pending.reject(new Error("signer export failed"));
      });
    });
  }

  async terminate(error) {
    const worker = this.worker;
    this.worker = null;
    this.rejectPending(error);
    if (!worker) return;
    const termination = worker.terminate();
    this.terminating = termination;
    try {
      await termination;
    } finally {
      if (this.terminating === termination) this.terminating = null;
    }
  }

  rejectPending(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
  }
}

function serializeGraph(graph) {
  return { entry: graph.entry, files: [...graph.files].map(([name, file]) => [name, file]) };
}

function timeoutError(message) {
  const error = new Error(message);
  error.code = "signer_timeout";
  return error;
}
