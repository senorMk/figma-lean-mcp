// Plugin main thread — entry point, UI bootstrap, and request dispatch.
// The WebSocket lives in the UI iframe (only the iframe has network access);
// this thread receives relayed server requests, runs the handler, and posts the
// response back for the UI to send over the socket.

import { handleRequest } from "./handlers";

const sendStatus = () => {
  figma.ui.postMessage({
    type: "plugin-status",
    payload: {
      fileName: figma.root.name,
      pageName: figma.currentPage.name,
      selectionCount: figma.currentPage.selection.length,
    },
  });
};

const dispatch = async (request: any) => {
  try {
    return await handleRequest(request);
  } catch (error) {
    return {
      type: request.type,
      requestId: request.requestId,
      error: error instanceof Error ? error.message : String(error),
    };
  }
};

figma.showUI(__html__, { width: 300, height: 220 });
sendStatus();
figma.on("selectionchange", sendStatus);
figma.on("currentpagechange", sendStatus);

figma.ui.onmessage = async (message) => {
  if (message.type === "ui-ready") {
    sendStatus();
    return;
  }
  if (message.type === "get_ws_config") {
    const config = await figma.clientStorage.getAsync("ws_config");
    figma.ui.postMessage({
      type: "ws_config",
      host: config?.host ?? "127.0.0.1",
      port: config?.port ?? "1996",
    });
    return;
  }
  if (message.type === "save_ws_config") {
    await figma.clientStorage.setAsync("ws_config", {
      host: message.host,
      port: message.port,
    });
    return;
  }
  if (message.type === "server-request") {
    const response = await dispatch(message.payload);
    figma.ui.postMessage(response);
  }
};
