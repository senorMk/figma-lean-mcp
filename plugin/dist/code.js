(function() {
  "use strict";
  const jsonSafe = (value) => {
    if (value === void 0) return null;
    try {
      return JSON.parse(JSON.stringify(value));
    } catch (e) {
      try {
        return String(value);
      } catch (e2) {
        return "[unserializable]";
      }
    }
  };
  const round = (n) => Math.round(n * 100) / 100;
  const getBounds = (n) => {
    var _a, _b;
    return "width" in n ? { x: round((_a = n.x) != null ? _a : 0), y: round((_b = n.y) != null ? _b : 0), width: round(n.width), height: round(n.height) } : void 0;
  };
  const serializeShallow = (n) => {
    const obj = { id: n.id, name: n.name, type: n.type };
    const bounds = getBounds(n);
    if (bounds) obj.bounds = bounds;
    if (n.type === "TEXT") {
      obj.characters = n.characters;
      if (typeof n.fontSize === "number") obj.fontSize = n.fontSize;
    }
    return obj;
  };
  const applyFields = (obj, fields) => {
    if (!fields || !obj || typeof obj !== "object") return obj;
    const out = {};
    for (const k of fields) if (k in obj) out[k] = obj[k];
    if ("children" in obj) out.children = obj.children;
    if ("childCount" in obj) out.childCount = obj.childCount;
    return out;
  };
  const walk = (n, depthLeft, fields) => {
    const obj = serializeShallow(n);
    if ("children" in n && n.children.length > 0) {
      if (depthLeft <= 0) {
        obj.childCount = n.children.length;
      } else {
        obj.children = n.children.filter((c) => c.type !== "DOCUMENT").map((c) => walk(c, depthLeft - 1, fields));
      }
    }
    return applyFields(obj, fields);
  };
  const handleRequest = async (request) => {
    const reply = (data) => ({ type: request.type, requestId: request.requestId, data });
    switch (request.type) {
      case "run_code": {
        const code = request.params && request.params.code;
        if (typeof code !== "string") throw new Error("code (string) is required for run_code");
        try {
          const fn = new Function(
            "figma",
            '"use strict"; return (async () => {' + code + "\n})()"
          );
          const result = await fn(figma);
          try {
            figma.commitUndo();
          } catch (e) {
          }
          return reply(jsonSafe(result));
        } catch (error) {
          return reply({
            error: error instanceof Error ? error.message : String(error),
            stack: error instanceof Error ? error.stack : void 0
          });
        }
      }
      case "get_node": {
        const nodeId = request.nodeIds && request.nodeIds[0];
        if (!nodeId) throw new Error("nodeId is required for get_node");
        const node = await figma.getNodeByIdAsync(nodeId);
        if (!node || node.type === "DOCUMENT") throw new Error(`Node not found: ${nodeId}`);
        const params = request.params || {};
        const depth = params.depth != null ? Number(params.depth) : 1;
        const fields = Array.isArray(params.fields) && params.fields.length > 0 ? params.fields : null;
        if (depth < 0) {
          const full = (n) => {
            const obj = serializeShallow(n);
            if ("children" in n && n.children.length > 0) {
              obj.children = n.children.filter((c) => c.type !== "DOCUMENT").map(full);
            }
            return applyFields(obj, fields);
          };
          return reply(full(node));
        }
        return reply(walk(node, depth, fields));
      }
      case "get_document": {
        const params = request.params || {};
        const depth = params.depth != null ? Number(params.depth) : 1;
        const page = figma.currentPage;
        return reply({
          id: page.id,
          name: page.name,
          type: page.type,
          childCount: page.children.length,
          children: page.children.map((c) => walk(c, depth - 1, null))
        });
      }
      case "get_pages":
        return reply({
          currentPageId: figma.currentPage.id,
          pages: figma.root.children.map((p) => ({ id: p.id, name: p.name }))
        });
      case "get_screenshot": {
        const scale = request.params && request.params.scale != null ? Number(request.params.scale) : 2;
        const nodeId = request.nodeIds && request.nodeIds[0];
        const node = nodeId ? await figma.getNodeByIdAsync(nodeId) : figma.currentPage.selection[0];
        if (!node || node.type === "DOCUMENT" || node.type === "PAGE") {
          throw new Error("No exportable node. Provide a valid nodeId.");
        }
        const bytes = await node.exportAsync({
          format: "PNG",
          constraint: { type: "SCALE", value: scale }
        });
        return reply({
          nodeId: node.id,
          nodeName: node.name,
          width: node.width,
          height: node.height,
          base64: figma.base64Encode(bytes)
        });
      }
      default:
        throw new Error(`Unknown request type: ${request.type}`);
    }
  };
  const sendStatus = () => {
    figma.ui.postMessage({
      type: "plugin-status",
      payload: {
        fileName: figma.root.name,
        pageName: figma.currentPage.name,
        selectionCount: figma.currentPage.selection.length
      }
    });
  };
  const dispatch = async (request) => {
    try {
      return await handleRequest(request);
    } catch (error) {
      return {
        type: request.type,
        requestId: request.requestId,
        error: error instanceof Error ? error.message : String(error)
      };
    }
  };
  figma.showUI(__html__, { width: 300, height: 200 });
  sendStatus();
  figma.on("selectionchange", sendStatus);
  figma.on("currentpagechange", sendStatus);
  figma.ui.onmessage = async (message) => {
    var _a, _b;
    if (message.type === "ui-ready") {
      sendStatus();
      return;
    }
    if (message.type === "get_ws_config") {
      const config = await figma.clientStorage.getAsync("ws_config");
      figma.ui.postMessage({
        type: "ws_config",
        host: (_a = config == null ? void 0 : config.host) != null ? _a : "127.0.0.1",
        port: (_b = config == null ? void 0 : config.port) != null ? _b : "1996"
      });
      return;
    }
    if (message.type === "save_ws_config") {
      await figma.clientStorage.setAsync("ws_config", {
        host: message.host,
        port: message.port
      });
      return;
    }
    if (message.type === "server-request") {
      const response = await dispatch(message.payload);
      figma.ui.postMessage(response);
    }
  };
})();
