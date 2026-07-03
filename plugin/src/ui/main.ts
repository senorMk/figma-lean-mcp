// UI iframe — owns the WebSocket to the Go server and relays frames both ways:
//   server  --ws-->  UI  --postMessage-->  plugin core   (handleRequest)
//   plugin core  --postMessage-->  UI  --ws-->  server    (response)

const $ = (id: string) => document.getElementById(id)!;

let host = "127.0.0.1";
let port = "1996";
let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let configLoaded = false;
const RECONNECT_MS = 1500;

function setConnected(on: boolean) {
  const badge = $("badge");
  badge.className = "badge " + (on ? "on" : "off");
  $("dot").className = "dot" + (on ? " on" : "");
  $("status").textContent = on ? "Connected" : "Disconnected";
}

function connect() {
  if (socket) {
    socket.onclose = null;
    socket.close();
  }
  const ws = new WebSocket(`ws://${host}:${port}/ws`);
  socket = ws;

  ws.onopen = () => {
    setConnected(true);
    parent.postMessage({ pluginMessage: { type: "ui-ready" } }, "*");
  };
  ws.onerror = () => setConnected(false);
  ws.onclose = () => {
    if (socket !== ws) return; // stale — a newer connect() took over
    setConnected(false);
    socket = null;
    if (reconnectTimer === null) {
      reconnectTimer = setTimeout(() => {
        reconnectTimer = null;
        connect();
      }, RECONNECT_MS);
    }
  };
  ws.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data);
      // Relay the server request to the plugin core to execute.
      parent.postMessage({ pluginMessage: { type: "server-request", payload } }, "*");
    } catch {
      /* ignore malformed frames */
    }
  };
}

// Messages FROM the plugin core.
window.addEventListener("message", (event) => {
  const msg = (event as MessageEvent).data?.pluginMessage;
  if (!msg) return;

  if (msg.type === "ws_config") {
    host = msg.host ?? "127.0.0.1";
    port = msg.port ?? "1996";
    $("addrBtn").textContent = `${host}:${port}`;
    if (!configLoaded) {
      configLoaded = true;
      connect();
    }
    return;
  }
  if (msg.type === "plugin-status") {
    $("file").textContent = msg.payload.fileName ?? "—";
    $("page").textContent = msg.payload.pageName ?? "—";
    $("sel").textContent = `${msg.payload.selectionCount ?? 0} node(s)`;
    return;
  }
  if ("requestId" in msg) {
    // A handler response — forward it to the server.
    if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(msg));
  }
});

// Settings UI.
$("addrBtn").addEventListener("click", () => {
  (<HTMLInputElement>$("host")).value = host;
  (<HTMLInputElement>$("port")).value = port;
  $("settings").classList.add("show");
  $("addrBtn").style.display = "none";
});
function apply() {
  host = (<HTMLInputElement>$("host")).value.trim() || "127.0.0.1";
  const p = parseInt((<HTMLInputElement>$("port")).value, 10);
  port = p > 0 && p <= 65535 ? String(p) : "1996";
  $("addrBtn").textContent = `${host}:${port}`;
  $("settings").classList.remove("show");
  $("addrBtn").style.display = "";
  parent.postMessage({ pluginMessage: { type: "save_ws_config", host, port } }, "*");
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  connect();
}
$("apply").addEventListener("click", apply);
$("cancel").addEventListener("click", () => {
  $("settings").classList.remove("show");
  $("addrBtn").style.display = "";
});

// Load stored config from the plugin core; connect once it responds.
parent.postMessage({ pluginMessage: { type: "get_ws_config" } }, "*");
setTimeout(() => {
  if (!configLoaded) {
    configLoaded = true;
    connect();
  }
}, 500);
