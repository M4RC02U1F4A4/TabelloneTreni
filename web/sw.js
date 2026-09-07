/* Service worker minimo: serve a far comparire l'app anche quando la rete non
   c'è, non a farla funzionare offline — un tabellone senza rete non ha niente
   da dire.

   La strategia è "prima la rete, la cache solo se manca": servire dalla cache
   e riscaricare dopo mostrava sempre la versione precedente, e su iOS, dove
   l'app resta viva in background per giorni, quella precedente restava lì. */

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

  // I dati vivi non passano mai dalla cache: un tabellone vecchio è peggio di
  // nessun tabellone, e lo stesso vale per la posizione di un treno o per il
  // semaforo di una linea. La regola è scritta al contrario — si tiene solo
  // l'elenco delle stazioni, che cambia quando cambia l'immagine — così un
  // endpoint nuovo nasce fuori dalla cache invece che dentro, che è il verso
  // giusto in cui sbagliare.
  if (url.pathname.includes('/api/') && !url.pathname.endsWith('/api/stations')) return;

  e.respondWith(caches.open(CACHE).then(async (cache) => {
    try {
      const r = await fetch(req);
      if (r.ok) cache.put(req, r.clone());
      return r;
    } catch (err) {
      return (await cache.match(req)) || Promise.reject(err);
    }
  }));
});

/* ---------------------------------------------------------------- notifiche */

/* Il messaggio arriva cifrato dal servizio push e lo decifra il browser: qui
   dentro è già JSON. Se manca — certi servizi push mandano un colpo a vuoto per
   tenere viva la connessione — si mostra comunque qualcosa, perché il permesso
   è stato dato con `userVisibleOnly` e non mostrare niente lo fa revocare. */
self.addEventListener('push', (e) => {
  let m = {};
  try { m = e.data ? e.data.json() : {}; } catch { /* non era JSON */ }
  e.waitUntil(self.registration.showNotification(m.titolo || 'Tabellone Treni', {
    body: m.corpo || 'Lo stato di una linea è cambiato.',
    icon: 'icona-180.png',
    badge: 'icona-180.png',
    // Due notizie sulla stessa linea non si impilano: la seconda sostituisce la
    // prima, perché è la seconda quella vera.
    tag: m.tag || 'linee',
    data: { url: m.url || './#/linee' },
  }));
});

self.addEventListener('notificationclick', (e) => {
  e.notification.close();
  const url = (e.notification.data && e.notification.data.url) || './#/linee';
  // Se l'app è già aperta si porta in primo piano quella, invece di aprirne una
  // seconda copia: su iOS la finestra è una sola e aprirne un'altra la
  // ricaricherebbe da capo.
  e.waitUntil(self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    .then((aperte) => {
      for (const c of aperte) {
        if ('focus' in c) { c.navigate(url).catch(() => {}); return c.focus(); }
      }
      return self.clients.openWindow(url);
    }));
});
