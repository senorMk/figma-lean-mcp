// Request handlers. The plugin dispatches on request.type and returns
// { type, requestId, data } (or { ...error }). Matched to the server request
// by requestId.

// ── serialization helpers ─────────────────────────────────────────────────────

// jsonSafe makes an arbitrary run_code return value survive structured-clone
// (postMessage) and JSON encoding. Figma node objects are circular, so anything
// that fails a JSON round-trip falls back to its String() form.
const jsonSafe = (value: any): any => {
  if (value === undefined) return null;
  try {
    return JSON.parse(JSON.stringify(value));
  } catch {
    try {
      return String(value);
    } catch {
      return "[unserializable]";
    }
  }
};

const round = (n: number) => Math.round(n * 100) / 100;

const getBounds = (n: any) =>
  "width" in n
    ? { x: round(n.x ?? 0), y: round(n.y ?? 0), width: round(n.width), height: round(n.height) }
    : undefined;

// Compact per-node serialization: just enough to locate and identify a node.
const serializeShallow = (n: any): any => {
  const obj: any = { id: n.id, name: n.name, type: n.type };
  const bounds = getBounds(n);
  if (bounds) obj.bounds = bounds;
  if (n.type === "TEXT") {
    obj.characters = n.characters;
    if (typeof n.fontSize === "number") obj.fontSize = n.fontSize;
  }
  return obj;
};

// applyFields whitelists top-level keys but always keeps the tree keys.
const applyFields = (obj: any, fields: string[] | null): any => {
  if (!fields || !obj || typeof obj !== "object") return obj;
  const out: any = {};
  for (const k of fields) if (k in obj) out[k] = obj[k];
  if ("children" in obj) out.children = obj.children;
  if ("childCount" in obj) out.childCount = obj.childCount;
  return out;
};

const walk = (n: any, depthLeft: number, fields: string[] | null): any => {
  const obj = serializeShallow(n);
  if ("children" in n && n.children.length > 0) {
    if (depthLeft <= 0) {
      obj.childCount = n.children.length;
    } else {
      obj.children = n.children
        .filter((c: any) => c.type !== "DOCUMENT")
        .map((c: any) => walk(c, depthLeft - 1, fields));
    }
  }
  return applyFields(obj, fields);
};

// ── dispatch ──────────────────────────────────────────────────────────────────

export const handleRequest = async (request: any): Promise<any> => {
  const reply = (data: any) => ({ type: request.type, requestId: request.requestId, data });

  switch (request.type) {
    case "run_code": {
      const code = request.params && request.params.code;
      if (typeof code !== "string") throw new Error("code (string) is required for run_code");
      try {
        // Run the snippet as the body of an async function with `figma` in scope,
        // so `await` works and an explicit `return` yields the result.
        const fn = new Function(
          "figma",
          '"use strict"; return (async () => {' + code + "\n})()",
        );
        const result = await fn(figma);
        try {
          figma.commitUndo();
        } catch {
          /* commitUndo unavailable in some contexts — ignore */
        }
        return reply(jsonSafe(result));
      } catch (error: any) {
        return reply({
          error: error instanceof Error ? error.message : String(error),
          stack: error instanceof Error ? error.stack : undefined,
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
      const fields: string[] | null =
        Array.isArray(params.fields) && params.fields.length > 0 ? params.fields : null;

      if (depth < 0) {
        // Full subtree.
        const full = (n: any): any => {
          const obj = serializeShallow(n);
          if ("children" in n && n.children.length > 0) {
            obj.children = n.children
              .filter((c: any) => c.type !== "DOCUMENT")
              .map(full);
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
        children: page.children.map((c: any) => walk(c, depth - 1, null)),
      });
    }

    case "get_pages":
      return reply({
        currentPageId: figma.currentPage.id,
        pages: figma.root.children.map((p) => ({ id: p.id, name: p.name })),
      });

    case "get_screenshot": {
      const scale =
        request.params && request.params.scale != null ? Number(request.params.scale) : 2;
      const nodeId = request.nodeIds && request.nodeIds[0];
      const node = nodeId
        ? await figma.getNodeByIdAsync(nodeId)
        : figma.currentPage.selection[0];
      if (!node || node.type === "DOCUMENT" || node.type === "PAGE") {
        throw new Error("No exportable node. Provide a valid nodeId.");
      }
      const bytes = await (node as any).exportAsync({
        format: "PNG",
        constraint: { type: "SCALE", value: scale },
      });
      return reply({
        nodeId: node.id,
        nodeName: node.name,
        width: (node as any).width,
        height: (node as any).height,
        base64: figma.base64Encode(bytes),
      });
    }

    default:
      throw new Error(`Unknown request type: ${request.type}`);
  }
};
