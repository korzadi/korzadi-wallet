/* Korzadi service worker: solo cachea interfaz pública y estáticos; nunca API, admin, ni rutas autenticadas. */
var V="kz-v2", SHELL=["/"];
self.addEventListener("install", function(e){
  e.waitUntil(caches.open(V).then(function(c){ return c.addAll(SHELL); }).then(function(){ return self.skipWaiting(); }));
});
self.addEventListener("activate", function(e){
  e.waitUntil(caches.keys().then(function(k){ return Promise.all(k.filter(function(x){ return x!==V; }).map(function(x){ return caches.delete(x); })); }).then(function(){ return self.clients.claim(); }));
});
self.addEventListener("fetch", function(e){
  var r = e.request, u = new URL(r.url);
  // Solo cachear GET del mismo origen
  if (r.method !== "GET" || u.origin !== location.origin) return;
  // Excluir: API, admin, rutas autenticadas de wallet
  var excluded = ["/api/", "/admin"];
  var authPaths = ["/dashboard", "/assets", "/p2p", "/transactions", "/profile"];
  for (var i = 0; i < excluded.length; i++) { if (u.pathname.indexOf(excluded[i]) === 0) return; }
  for (var i = 0; i < authPaths.length; i++) { if (u.pathname.indexOf(authPaths[i]) === 0) return; }
  // No cachear el propio service worker ni archivos sensibles
  if (u.pathname.indexOf("/sw.js") === 0) return;
  // Respuesta para navegación
  if (r.mode === "navigate") {
    e.respondWith(
      fetch(r).then(function(res){
        var c = res.clone();
        caches.open(V).then(function(x){ x.put(r, c); });
        return res;
      }).catch(function(){ return caches.match(r).then(function(m){ return m || caches.match("/"); }); })
    );
    return;
  }
  // Cachear estáticos de la interfaz pública
  e.respondWith(
    caches.match(r).then(function(m){
      return m || fetch(r).then(function(res){
        if (res.ok) {
          var c = res.clone();
          caches.open(V).then(function(x){ x.put(r, c); });
        }
        return res;
      });
    })
  );
});
