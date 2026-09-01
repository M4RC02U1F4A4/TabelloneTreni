/* Service worker minimo: serve a far comparire l'app subito quando la si apre
   dalla home del telefono, non a farla funzionare offline — un tabellone senza
   rete non ha niente da dire.

   La strategia è "servi dalla cache e intanto riscarica": l'apertura è
   istantanea e la versione successiva è già aggiornata, senza dover numerare a
   mano una cache a ogni modifica. */

const CACHE = 'tabellone';
const GUSCIO = ['.', 'index.html', 'app.css', 'app.js', 'icona.svg', 'manifest.webmanifest'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(GUSCIO)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((nomi) => Promise.all(nomi.filter((n) => n !== CACHE).map((n) => caches.delete(n))))
      .then(() => self.clients.claim()));
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== location.origin) return;

  // Un tabellone vecchio è peggio di nessun tabellone: questa richiesta non
  // viene mai servita dalla cache, e se la rete manca l'app lo dice.
  if (url.pathname.endsWith('/api/board')) return;

  e.respondWith(caches.open(CACHE).then(async (cache) => {
    const salvata = await cache.match(req, { ignoreSearch: false });
    const dalla_rete = fetch(req).then((r) => {
      if (r.ok) cache.put(req, r.clone());
      return r;
    }).catch(() => salvata);
    return salvata || dalla_rete;
  }));
});
