// A disposable worker keeps passwords and OPAQUE ephemeral secrets out of the UI and persisted state.
importScripts("wasm_exec.js");
const go = new Go();
const ready = fetch("client.wasm", { cache: "no-cache" })
  .then((r) => { if (!r.ok) throw new Error("无法加载密码登录组件"); return r.arrayBuffer(); })
  .then((bytes) => WebAssembly.instantiate(bytes, go.importObject))
  .then(({ instance }) => { go.run(instance); });
self.onmessage = async ({ data }) => {
  try {
    await ready;
    const result = JSON.parse(self.personaPake(JSON.stringify(data.input)));
    self.postMessage({ id: data.id, ...result });
  } catch {
    self.postMessage({ id: data.id, error: "无法加载密码登录组件，请刷新后重试" });
  }
};
