/* Le schermate dell'app con dati finti, misurate: niente scorrimento
 * orizzontale, niente scroller interno che non dovrebbe esserci, e uno
 * screenshot per tema. Gira solo in locale — i runner di CI non hanno un
 * browser — contro un server già avviato su :18080:
 *   go build -o /tmp/tt . && PORT=18080 STATO_LINEE_URL=http://localhost:1 /tmp/tt &
 *   PW=~/.npm/_npx/6bcb61ec6d5aea22/node_modules/playwright node ci/schermate.js
 */
const path = require('node:path');
const fs = require('node:fs');

// La mezzanotte di Roma di oggi, come la salva l'app nel segnalibro.
const giornoRoma = new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Rome' }).format(new Date());
const MEZZANOTTE = new Date(`${giornoRoma}T00:00:00+02:00`).getTime();

// Nomi lunghi apposta: sono quelli che allargano una pagina.
const viaggio = {
  id: { origin: 'S01322', number: '24854', date: MEZZANOTTE },
  number: '24854', category: 'S8', origin: 'MONZA', terminus: 'MALPENSA AEROPORTO TERMINAL 2',
  tracked: true, delay: 19, disrupted: true, suppressedStops: 1,
  lastSeen: { station: '1°BIVIO FIDENZA OVEST', time: '08:00' },
  row: { number: '24854', category: 'S8', terminus: 'MALPENSA AEROPORTO TERMINAL 2', time: '08:12',
    delay: 15, liveDelay: 19, platform: '21', platformChanged: true, platformScheduled: '19',
    platformActual: '21', arrival: '08:31', notes: 'CARROZZA 1 IN CODA AL TRENO - GATE B' },
  stops: [
    { name: 'MONZA', scheduled: '08:12', platform: '21', platformScheduled: '19', boarding: true, lat: 45.57, lon: 9.27 },
    { name: 'SESTO S.GIOVANNI', scheduled: '08:16', platform: '2', lat: 45.53, lon: 9.23 },
    { name: 'MILANO PORTA GARIBALDI', scheduled: '08:31', platform: '15', chosen: true, lat: 45.48, lon: 9.19 },
    { name: 'MALPENSA AEROPORTO TERMINAL 2', scheduled: '09:10', platform: '1', lat: 45.63, lon: 8.72 },
  ],
};

const segnalibro = { o: 'S01322', n: '24854', d: MEZZANOTTE, cat: 'S8', capolinea: 'MALPENSA AEROPORTO TERMINAL 2', f: 1841, a: 1715 };

const VISTE = {
  home: { rotta: '#/', dopo: async (p) => { await p.waitForTimeout(3000); } },
  'home-modifica': { rotta: '#/', dopo: async (p) => { await p.waitForTimeout(3000); await p.click('[data-modifica]').catch(() => {}); } },
  tratta: { rotta: '#/p/1841/1715', dopo: async (p) => { await p.waitForTimeout(3500); await p.click('.tabella.tratta details[data-treno] summary').catch(() => {}); await p.waitForTimeout(2500); } },
  tabellone: { rotta: '#/p/1728', dopo: async (p) => { await p.waitForTimeout(3500); } },
  treno: { rotta: `#/t/S01322/24854/${MEZZANOTTE}`, dopo: async (p) => { await p.waitForTimeout(2000); await p.click('.precedenti summary').catch(() => {}); } },
  linee: { rotta: '#/linee', dopo: async (p) => { await p.waitForTimeout(2500); } },
};

async function apri(page, vista) {
  await page.route('**/api/journey**', (r) => r.fulfill({ json: viaggio }));
  await page.addInitScript((s) => {
    localStorage.setItem('tt.preferiti', JSON.stringify([{ f: 1841, t: 1715 }, { f: 1728, t: 1715 }, { f: 2416, a: true }]));
    localStorage.setItem('tt.campi', JSON.stringify({ da: 1841, a: 1715 }));
    localStorage.setItem('tt.seguiti', JSON.stringify([s]));
    localStorage.setItem('tt.abituali', JSON.stringify([{ o: 'S01645', n: '24868', f: 1715, at: '17:42', days: [1, 2, 3, 4, 5], cat: 'S8', capolinea: 'MONZA' }]));
  }, segnalibro);
  await page.goto('http://localhost:18080/' + VISTE[vista].rotta);
  await VISTE[vista].dopo(page);
}

/* Due misure: la pagina non è più larga della finestra, e nessun elemento
 * sporge a destra oltre la finestra. La seconda trova il colpevole. */
async function controllaOverflow(page) {
  return page.evaluate(() => {
    const colpevoli = [];
    if (document.documentElement.scrollWidth > innerWidth) colpevoli.push('html');
    // Le file che scorrono di lato (.gettoni, .filtri) sporgono apposta: chi
    // sta dentro non è un colpevole. Lo scroller invece resta misurato, perché
    // se è lui a uscire dalla finestra allora la pagina è davvero più larga.
    const dentroScroller = (el) => {
      for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
        if (/auto|scroll/.test(getComputedStyle(p).overflowX)) return true;
      }
      return false;
    };
    for (const el of document.querySelectorAll('body *')) {
      const r = el.getBoundingClientRect();
      if (r.width && r.right > innerWidth + 1 && !dentroScroller(el)) {
        let id = el.tagName.toLowerCase();
        const classi = typeof el.className === 'string' ? el.className.trim() : '';
        if (classi) id += '.' + classi.split(/\s+/).join('.');
        else {
          // Senza classi il solo tag (`button`) non dice quale sia: servono
          // gli attributi data-* e un pezzo di testo.
          for (const [k, v] of Object.entries(el.dataset)) id += `[data-${k.replace(/[A-Z]/g, (c) => '-' + c.toLowerCase())}=${v}]`;
          const testo = el.textContent.trim().replace(/\s+/g, ' ').slice(0, 30);
          if (testo) id += `"${testo}"`;
        }
        colpevoli.push(id);
      }
    }
    return [...new Set(colpevoli)].slice(0, 8);
  });
}

// Il giro completo parte solo se il file è lanciato direttamente: i task che
// fanno `require` vogliono apri e controllaOverflow, non un browser che misura
// tutto e chiude il processo. Anche Playwright si carica solo qui: le due
// funzioni ricevono la pagina da chi le chiama.
if (require.main === module) (async () => {
  const { chromium } = require(process.env.PW || 'playwright');
  const out = process.argv[2] || path.join(__dirname, '..', '.render', 'schermate');
  fs.mkdirSync(out, { recursive: true });
  const b = await chromium.launch();
  let errori = 0;
  for (const vista of Object.keys(VISTE)) {
    for (const tema of ['dark', 'light']) {
      for (const width of [360, 390]) {
        const ctx = await b.newContext({ viewport: { width, height: 844 }, deviceScaleFactor: 2, colorScheme: tema });
        const page = await ctx.newPage();
        const pageErrors = [];
        page.on('pageerror', (e) => pageErrors.push(e.message));
        await apri(page, vista);
        const colpevoli = await controllaOverflow(page);
        if (width === 390) await page.screenshot({ path: path.join(out, `${vista}-${tema}.png`), fullPage: true });
        if (colpevoli.length) console.log(`OVERFLOW ${vista} ${tema} ${width} ${colpevoli.join(' ')}`);
        if (pageErrors.length) console.log(`ERRORE ${vista} ${tema} ${width} ${pageErrors.join(' | ')}`);
        if (colpevoli.length || pageErrors.length) errori++;
        else console.log(`OK ${vista} ${tema} ${width}`);
        await ctx.close();
      }
    }
  }
  await b.close();
  process.exit(errori ? 1 : 0);
})();

module.exports = { apri, controllaOverflow, viaggio, segnalibro, MEZZANOTTE };
