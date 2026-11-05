// lukky.js connects an overlay page to Lukky. Load it from /lukky.js:
//
//   <script src="/lukky.js"></script>
//   <script>
//     lukky.on("alert", (data) => show(data));
//   </script>
//
// The page must be served from /overlays/<module>/. Handlers for stored
// values (host.overlay.set) run again with the current value after reconnects.
(() => {
  const module = location.pathname.split("/")[2];
  const source = new EventSource(`/overlays/${encodeURIComponent(module)}/events`);
  const handlers = new Map();
  window.lukky = {
    module,
    on(event, handler) {
      if (!handlers.has(event)) {
        handlers.set(event, []);
        source.addEventListener(event, (e) => {
          const data = JSON.parse(e.data);
          for (const h of handlers.get(event)) h(data);
        });
      }
      handlers.get(event).push(handler);
    },
  };
})();
