/* I controlli sul frontend: `node ci/app.test.js`.
 *
 * app.js è scritto per il browser e non esporta niente: qui se ne legge il
 * sorgente e si valutano i pezzi che servono, così i controlli girano sul
 * codice vero invece che su una copia destinata a divergere.
 *
 * Sta in ci/ e non accanto ad app.js perché main.go fa `//go:embed all:web`:
 * là dentro finirebbe nel binario, e verrebbe servito come una pagina. */
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const src = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.js'), 'utf8');

// Ritaglia dal sorgente il pezzo fra due ancore, e fallisce se non lo trova:
// se qualcuno sposta o rinomina, il controllo lo dice invece di sparire.
function ritaglia(da, a) {
  const i = src.indexOf(da);
  const j = src.indexOf(a);
  assert.ok(i >= 0 && j > i, `ancore non trovate in app.js: ${da} … ${a}`);
  return src.slice(i, j);
}

/* ---------------------------------------------------- titolo() sui nomi RFI */

const titolo = new Function(`${ritaglia('const MINORI', '/* Segna nel nome')}; return titolo;`)();

for (const [dato, atteso] of [
  ['MILANO PORTA GARIBALDI', 'Milano Porta Garibaldi'],
  ['LA SPEZIA CENTRALE', 'La Spezia Centrale'],
  ['ALBA ADRIATICA-NERETO-CONTROGUERRA', 'Alba Adriatica-Nereto-Controguerra'],
  ['S.MARIA LA LONGA', 'S.Maria la Longa'],
  ['ANZOLA DELL\'EMILIA', 'Anzola dell\'Emilia'],
  ['BORGHETTO SULL\'ADIGE', 'Borghetto sull\'Adige'],
  ['BIVIO D\'AURISINA', 'Bivio d\'Aurisina'],
  ['STAZIONE PER L\'ALPAGO', 'Stazione per l\'Alpago'],
  ['MILANO P.TA GARIBALDI', 'Milano P.ta Garibaldi'],   // troncamento: minuscolo
  ['BOLOGNA C.LE/AV', 'Bologna C.le/AV'],
  ['ACQUEDOLCI-S.FRATELLO', 'Acquedolci-S.Fratello'],   // iniziale puntata: no
  ['COLLEVALENZA R.R.', 'Collevalenza R.R.'],
  ['PM S.LEONARDO DI CUTRO', 'PM S.Leonardo di Cutro'], // sigla di esercizio
  ['NAPOLI AFRAGOLA PES', 'Napoli Afragola PES'],
  ['CUZZAGO (MI)', 'Cuzzago (MI)'],                     // sigla di provincia
  ['COSTIGLIOLE (MOTTA DI)', 'Costigliole (Motta di)'],
  ['CANICATTI\'', 'Canicatti\''],                       // accento non indovinato
  ['CASSANO D`ADDA', 'Cassano d`Adda'],                 // l'apostrofo storto di RFI
]) {
  assert.strictEqual(titolo(dato), atteso, dato);
}

// Sul catalogo intero: la lunghezza non cambia mai, e le uniche cose che
// restano urlate sono le sigle dichiarate.
const catalogo = path.join(__dirname, '..', 'internal', 'stations', 'stations.json');
const nomi = JSON.parse(fs.readFileSync(catalogo, 'utf8')).stations.map((s) => s.n);
assert.ok(nomi.length > 2000, 'catalogo troppo corto, il file è cambiato?');

for (const n of nomi) {
  assert.strictEqual(titolo(n).length, n.length, `lunghezza cambiata su ${n}`);
  for (const t of titolo(n).match(/\b[A-Z]{2,}\b/g) || []) {
    assert.ok(['AV', 'MI', 'NO', 'PC', 'PES', 'PM'].includes(t), `${n} → resta urlato: ${t}`);
  }
}

console.log(`titolo: ok — 18 casi + ${nomi.length} stazioni del catalogo`);

/* ------------------------------------- la tratta aperta per ultima, in home */

/* preferitiOrdinati() riconosce il preferito da guardare confrontando la
 * chiave della tratta con quella salvata quando si apre un tabellone. Le due
 * chiavi nascono da due forme diverse — un preferito salvato e una rotta letta
 * dall'URL — e se smettessero di combaciare non si romperebbe niente a schermo:
 * la home resterebbe semplicemente nell'ordine di prima, in silenzio. */
const pezzoChiavi = ritaglia('const chiaveTratta =', 'function alternaPreferito');
const chiaveTratta = new Function(`${pezzoChiavi}; return chiaveTratta;`)();
const chiaveRotta = new Function(`${pezzoChiavi}; return chiaveRotta;`)();
const rottaDi = new Function(`${ritaglia('const rottaDi =', 'const ROTTA_LINEE')}; return rottaDi;`)();

// leggiRotta() legge location.hash: le si dà un location finto e per il resto
// gira il codice vero.
const leggiRotta = new Function('location',
  `${ritaglia('function leggiRotta()', 'const rottaDi =')}; return leggiRotta;`);

for (const preferito of [
  { f: 1393, t: 1715, a: undefined },        // Gallarate → Milano P. Garibaldi
  { f: 1715, t: 1393, a: undefined },        // il ritorno
  { f: 1393, t: null, a: undefined },        // tutte le partenze da Gallarate
  { f: 1393, t: null, a: true },             // gli arrivi a Gallarate
]) {
  // Il giro completo: dal preferito all'indirizzo su cui porta, da lì alla
  // rotta che il router legge, e da quella alla chiave che si salva.
  const r = leggiRotta({ hash: rottaDi(preferito.f, preferito.t, preferito.a) })();
  assert.strictEqual(r.vista, 'risultati', JSON.stringify(preferito));
  assert.strictEqual(
    chiaveRotta(r),
    chiaveTratta(preferito),
    `la chiave della rotta non combacia con quella del preferito: ${JSON.stringify(preferito)}`);
}

// Due tratte diverse non devono cadere sulla stessa chiave, altrimenti in cima
// finirebbe quella sbagliata.
const chiavi = [
  { f: 1393, t: 1715 }, { f: 1715, t: 1393 }, { f: 1393, t: null },
  { f: 1393, t: null, a: true }, { f: 1715, t: null },
].map(chiaveTratta);
assert.strictEqual(new Set(chiavi).size, chiavi.length, `chiavi in collisione: ${chiavi}`);

/* E ricordaTratta() se ne ricorda solo quando è una tratta salvata: una
 * ricerca al volo non deve cancellare quale preferito si stava usando. */
const salvate = [{ f: 1393, t: 1715 }, { f: 1715, t: 1393 }];
const nuovaRicorda = (rotta) => {
  let scritto;
  new Function('preferiti', 'scrivi',
    `${pezzoChiavi}; return ricordaTratta;`)(() => salvate, (_k, v) => { scritto = v; })(rotta);
  return scritto;
};

assert.strictEqual(
  nuovaRicorda({ da: 1715, a: 1393, arrivi: false }), '1715>1393',
  'una tratta salvata deve essere ricordata');
assert.strictEqual(
  nuovaRicorda({ da: 2263, a: null, arrivi: false }), undefined,
  'una ricerca al volo non deve toccare la tratta ricordata');
assert.strictEqual(
  nuovaRicorda({ da: 1393, a: null, arrivi: true }), undefined,
  'gli arrivi a una stazione salvata solo in partenza non sono quella tratta');

console.log('tratte: ok — 4 giri completi + 3 casi su ricordaTratta');

/* ------------------------------------------- il numero del binario e il SOT */

/* numeroBinario() è l'unico punto dell'app che compone HTML a pezzi invece di
 * passare tutto da esc(), quindi il controllo guarda due cose: che stacchi la
 * qualifica solo quando c'è davvero, e che quello che RFI ci mette dentro non
 * esca mai dal template. */
const esc = new Function(`${ritaglia('const esc =', '/* RFI manda i nomi')}; return esc;`)();
const numeroBinario = new Function('esc',
  `${ritaglia('function numeroBinario', '/* Il provvedimento')}; return numeroBinario;`)(esc);

for (const [dato, atteso] of [
  ['2', '2'],
  ['10', '10'],
  ['2 SOT', '2<small>SOT</small>'],        // i sotterranei di Porta Garibaldi
  ['1 SOT', '1<small>SOT</small>'],
  ['EST', 'EST'],                          // niente cifra davanti: tale e quale
  ['', ''],
]) {
  assert.strictEqual(numeroBinario(dato), atteso, dato);
}

// RFI scrive a mano in quella casella: qualunque cosa ci finisca esce escapata.
assert.strictEqual(numeroBinario('<b>x'), '&lt;b&gt;x');
assert.strictEqual(numeroBinario('3 <b>x'), '3<small>&lt;b&gt;x</small>');

// Sotto la cifra non deve più comparire "bin.", ma la didascalia deve tornare
// quando il binario cambia — e la parola resta per chi ascolta la pagina.
const cellaBinario = new Function('esc',
  `${ritaglia('function numeroBinario', '/* Il provvedimento')}; return cellaBinario;`)(esc);

const normale = cellaBinario('2', null);
assert.ok(!/bin\./.test(normale), 'la didascalia "bin." non va più scritta');
assert.ok(/solo-lettori">binario</.test(normale), 'la parola resta per lo screen reader');
assert.ok(!/class="cap"/.test(normale), 'senza cambio non c\'è didascalia visibile');

const cambiato = cellaBinario('5', 'era 3');
assert.ok(/class="cap">era 3</.test(cambiato), 'a binario cambiato la didascalia torna');
assert.ok(/binario cambiato"/.test(cambiato), 'e la cella si marca come cambiata');

assert.ok(/ignoto/.test(cellaBinario('', null)), 'senza binario resta il trattino');

console.log('binario: ok — 6 casi + 2 di escaping + 6 sulla cella');

/* ------------------------------------ la barretta che conta il minuto */

/* La barretta mente in silenzio se il ritardo negativo si scollega da quando
 * il dato è stato letto: resta piena, e chi guarda crede che il tabellone si
 * sia appena riletto. Nessuno se ne accorgerebbe guardando lo schermo. */
const pezzoFreschezza = ritaglia('function rigaFreschezza()', '/* La rilettura di un treno');
const nuovaFreschezza = (n) => new Function('stato', 'eta', 'RINFRESCO', 'VECCHIA',
  `${pezzoFreschezza}; return ${n};`);

const RINFRESCO = 60_000;
const VECCHIA = 2 * 60_000;
const contesto = (ms, caricamento = false) =>
  [{ caricamento, scaricatoIl: ms === null ? 0 : Date.now() - ms },
   () => `${Math.round(ms / 60_000)} minuti fa`, RINFRESCO, VECCHIA];

const barra = (ms, caricamento = false) => nuovaFreschezza('barraCiclo')(...contesto(ms, caricamento))();
const testo = (ms, caricamento = false) => nuovaFreschezza('rigaFreschezza')(...contesto(ms, caricamento))();

const ritardo = (html) => Number((html.match(/--trascorso:(-?\d+)ms/) || [])[1]);

// Appena letto: barretta piena, cioè nessuno scorrimento già consumato.
assert.ok(Math.abs(ritardo(barra(0))) < 50, 'appena letto la barretta parte piena');

// A metà minuto deve partire da metà, non da capo.
const meta = ritardo(barra(30_000));
assert.ok(meta < -29_000 && meta > -31_000, `a metà minuto il ritardo è ${meta}`);

// Oltre il minuto — l'app è stata in secondo piano — si ferma a vuota invece
// di ripartire: un giro in più direbbe che il dato è appena arrivato.
assert.strictEqual(ritardo(barra(5 * 60_000)), -RINFRESCO, 'oltre il minuto resta a fondo corsa');

// Durante una rilettura la barretta resta: toglierla la farebbe lampeggiare
// via e tornare a ogni minuto. Prima della prima lettura invece non c'è.
assert.ok(/ciclo/.test(barra(60_000, true)), 'in rilettura la barretta resta');
assert.strictEqual(barra(null), '', 'prima della prima lettura niente barretta');

// Fresco il testo tace: la barretta conta già quel minuto, e ripeterlo a
// parole sarebbe la stessa cosa detta due volte.
assert.strictEqual(testo(1000), '', 'fresco non si dice');
assert.strictEqual(testo(60_000), '', 'nemmeno a un minuto, che è cadenza normale');
// Vecchio invece sì: è il caso in cui tacere farebbe passare il vecchio per nuovo.
assert.strictEqual(testo(4 * 60_000), 'letto <span id="eta">4 minuti fa</span>',
  'vecchio si dice, e resta agganciato a #eta perché continui a scorrere');
// "aggiornamento…" non si scrive più: comparendo e sparendo ogni minuto
// portava via una riga e faceva ballare il contenuto sotto.
assert.strictEqual(testo(1000, true), '', 'in rilettura il sottotitolo tace');
assert.strictEqual(testo(null), '', 'prima della prima lettura pure');

console.log('freschezza: ok — 10 casi su barretta e testo');


/* ------------------------------------- le due sezioni delle comunicazioni */

/* La pagina di Trenord tiene due elenchi — "STATO DELLA LINEA" e "AVVISI" — e
   arrivano mescolati in una lista sola. Qui si controlla che nella riga di una
   linea finisca la sola circolazione, dal più recente, e con l'ora. */

const sezioni = new Function(
  `${ritaglia('const SEZIONE_AVVISI', 'function vociAvviso')}
   return { diCircolazione, quandoAvviso };`)();

// I cinque avvisi veri della RE_5 dell'8 settembre 2026, mescolati come li
// manda Trenord: tre di circolazione fuori ordine, due programmati.
const re5 = [
  { date: '2026-09-08T16:41:00Z', section: 2, text: '2536 non è ancora partito' },
  { date: '2026-09-08T16:46:54Z', section: 2, text: '2506 oggi non partirà' },
  { date: '2026-09-08T16:42:48Z', section: 2, text: '2504 viaggia in ritardo' },
  { date: '2026-09-02T12:58:00Z', section: 1, text: 'variazioni fino al 13 settembre' },
  { date: '2026-09-01T16:29:27Z', section: 1, text: 'sciopero CUB e SGB' },
];

const tenuti = sezioni.diCircolazione(re5);
assert.strictEqual(tenuti.length, 3, 'restano le sole tre di circolazione');
assert.ok(!tenuti.some((a) => a.section === 1), 'nessun programmato fra i tenuti');
// Il più recente in cima: 18:46, poi 18:42, poi 18:41.
assert.deepStrictEqual(tenuti.map((a) => a.text), [
  '2506 oggi non partirà',
  '2504 viaggia in ritardo',
  '2536 non è ancora partito',
], 'ordinate dalla più recente');

// Una sezione che non conosciamo si tiene: se la sorgente cambia sotto, una
// riga in più è meglio di una notizia scomparsa in silenzio.
assert.strictEqual(
  sezioni.diCircolazione([{ date: '2026-09-08T10:00:00Z', text: 'x' }]).length, 1,
  'senza sezione si tiene');
assert.strictEqual(
  sezioni.diCircolazione([{ date: '2026-09-08T10:00:00Z', section: 7, text: 'x' }]).length, 1,
  'sezione sconosciuta si tiene');

// Senza data va in fondo, non in cima: non è il candidato a essere il più
// recente solo perché non si sa quando è stato scritto.
const senzaData = sezioni.diCircolazione([
  { section: 2, text: 'senza data' },
  { date: '2026-09-08T16:41:00Z', section: 2, text: 'con data' },
]);
assert.deepStrictEqual(senzaData.map((a) => a.text), ['con data', 'senza data'],
  'quella senza data resta in fondo');

/* --------------------------------------------- l'ora delle comunicazioni */

const quando = new Function(
  `${ritaglia('function quandoScritto', '/* Lo scheletro tiene')}; return quandoScritto;`)();

// Di oggi la sola ora: il giorno lo si sa, e ripeterlo su tre righe di seguito
// era proprio ciò che le rendeva indistinguibili.
const oggi = new Date();
oggi.setHours(18, 46, 0, 0);
assert.strictEqual(quando({ date: oggi.toISOString() }), '18:46', 'di oggi solo l\'ora');

// Dei giorni prima il giorno e l'ora, come scrive Trenord.
//
// La data si costruisce dai componenti locali e non da un istante UTC fisso:
// scritta come "2026-09-02T12:58:00Z" l'attesa valeva solo su una macchina in
// ora italiana, e il controllo cadeva sul runner, che sta in UTC. Del giorno si
// verifica la forma e non il nome, che è quello che il fuso non può spostare.
const vecchio = new Date();
vecchio.setDate(vecchio.getDate() - 6);
vecchio.setHours(14, 58, 0, 0);
assert.match(quando({ date: vecchio.toISOString() }), /^\d{1,2} [a-zà-ù]+, 14:58$/,
  'dei giorni prima giorno e ora');

// Senza data, o con una data che non si legge, niente etichetta: meglio la
// riga senza data che una data inventata.
assert.strictEqual(quando({}), '', 'senza data niente etichetta');
assert.strictEqual(quando({ date: 'non una data' }), '', 'data illeggibile: niente');

console.log('sezioni: ok — 8 casi sul filtro e l\'ordine + 4 sull\'ora');

/* ------------------------------- la riga di un treno seguito */

/* La scheda di un treno seguito è la riga del suo tabellone: finché il treno è
   lì, quella vera, con le due letture del ritardo. Partito, dal tabellone
   sparisce e resta la sola misura sul treno. */

const scheda = new Function(`
  const prossimaFermata = (d) => (d.stops || []).find((f) => !f.passed) || null;
  ${ritaglia('function rigaSeguita', 'function schedaSeguito')}
  return rigaSeguita;`)();

// Con la riga del tabellone si usa quella, intatta: è già l'unione delle due
// fonti fatta dal server, e rifarla qui vorrebbe dire due verità.
const vera = { number: '24566', time: '19:34', delay: 3, liveDelay: 1, platform: '2 SOT' };
assert.strictEqual(scheda({ row: vera }), vera, 'con la riga si usa la riga');

// Partito: nessuna riga. L'ora è quella della fermata da cui si sale, non del
// capolinea da cui il treno viene.
const partito = scheda({
  tracked: true, delay: 7, number: '2536', category: 'REG', terminus: 'PORTO CERESIO',
  stops: [
    { name: 'LECCE', scheduled: '12:06', actual: '12:07', passed: true },
    { name: 'MILANO PORTA GARIBALDI', scheduled: '18:32', actual: '18:40', passed: true, boarding: true },
    { name: 'RHO FIERA', scheduled: '18:43', platform: '4', chosen: true },
  ],
});
assert.strictEqual(partito.senzaRFI, true, 'dichiara che del tabellone non sa niente');
assert.strictEqual(partito.time, '18:40', 'l\'ora è quella della salita, non dell\'origine');
assert.strictEqual(partito.liveDelay, 7, 'porta la misura sul treno');
assert.strictEqual(partito.platform, '4', 'il binario è quello della prossima fermata');
assert.strictEqual(partito.arrival, '18:43', 'l\'arrivo è alla fermata scelta');

// Non rilevato e senza riga: nessuna misura da nessuna delle due parti, e non
// se ne inventa una.
const muto = scheda({ tracked: false, delay: 0, stops: [{ scheduled: '19:00' }] });
assert.strictEqual(muto.liveDelay, undefined, 'senza rilevamento nessuna misura');

/* --------------------------- e le pastiglie su una riga senza tabellone */

const pastiglie = new Function(`
  const esc = (s) => String(s);
  ${ritaglia('const ritardoLive', 'const conMisure')}
  return { scarti, ritardoRFI };`)();

// Su una riga senza lettura RFI la pastiglia ambra non si scrive: uno zero
// sarebbe una puntualità che RFI non ha mai dichiarato.
assert.strictEqual(pastiglie.ritardoRFI({ senzaRFI: true }), null, 'senza tabellone: null');
assert.strictEqual(pastiglie.ritardoRFI({}), 0, 'col tabellone e senza numero: in orario');

const soloVT = pastiglie.scarti({ senzaRFI: true, liveDelay: 7 }, false);
assert.ok(/scarto misura/.test(soloVT) && /\+7/.test(soloVT), 'resta la misura sul treno');
assert.ok(!/scarto rfi/.test(soloVT), 'nessuna pastiglia del tabellone');
// Nessuna delle due: non si scrive niente, che è la verità.
assert.strictEqual(pastiglie.scarti({ senzaRFI: true }, false), '', 'senza misure niente');

// E il verso della misura sola, che è il colore: l'anticipo non è un ritardo
// col segno meno, e verde contro rosso è tutta la differenza che si legge.
assert.match(pastiglie.scarti({ senzaRFI: true, liveDelay: -4 }, false),
  /misura presto[^>]*>\s*-4</, 'in anticipo: verde e col segno');
assert.match(pastiglie.scarti({ senzaRFI: true, liveDelay: 0 }, false),
  /misura puntuale/, 'in orario: né rosso né verde');
assert.match(soloVT, /misura tardi/, 'in ritardo: rosso');

console.log('scheda seguita: ok — 6 casi sulla riga + 8 sulle pastiglie');

/* ------------------------------------------- distanze e proiezione */

const geo = new Function(`
  ${ritaglia('function metriFra', '/* Quanto dista una fermata')}
  ${ritaglia('function distanzaScritta', "/* Dov'è adesso")}
  return { metriFra, distanzaScritta };`)();

// Due stazioni di Milano che si guardano: Centrale e Porta Garibaldi sono
// poco più di un chilometro in linea d'aria.
const m = geo.metriFra(45.486347, 9.204528, 45.484917, 9.187683);
assert.ok(m > 1250 && m < 1400, `Centrale-Garibaldi = ${Math.round(m)} m`);

// E due che non si guardano affatto, per vedere che la formula tiene anche in
// grande: Milano-Lecce in linea d'aria sono 926 km — cinque gradi di
// latitudine fanno 572 km, nove di longitudine a quelle latitudini ne fanno
// 729, e l'ipotenusa è quella. Non la distanza per strada, che è di più.
const lontano = geo.metriFra(45.486347, 9.204528, 40.345660, 18.165724);
assert.ok(lontano > 915_000 && lontano < 935_000, `Milano-Lecce = ${Math.round(lontano / 1000)} km`);

// Lo stesso punto è a zero da sé: sembra ovvio, ma è il caso in cui una
// formula scritta male restituisce NaN per una radice di un negativo.
assert.strictEqual(Math.round(geo.metriFra(45.4, 9.2, 45.4, 9.2)), 0, 'da sé è zero');

/* La distanza si scrive come la si dice: il GPS non ha la precisione del metro
   e a nessuno serve sapere che sono 1348 metri. */
for (const [metri, atteso] of [
  // Sotto la precisione del GPS non si finge una cifra.
  [12, 'meno di 50 m'], [49, 'meno di 50 m'],
  [50, '50 m'], [120, '100 m'], [640, '650 m'], [949, '950 m'],
  // La soglia guarda l'arrotondato: attraversandola il numero non torna
  // indietro, che era il difetto — a 950 m scriveva "0,9 km".
  [975, '1,0 km'], [1348, '1,3 km'], [25_600, '25,6 km'],
]) {
  assert.strictEqual(geo.distanzaScritta(metri), atteso, `${metri} m`);
}
// E la scala non torna mai indietro, su tutto l'intervallo che conta.
let ultimo = 0;
for (let d = 50; d < 30_000; d += 7) {
  const km = geo.distanzaScritta(d).endsWith('km');
  const n = km ? parseFloat(geo.distanzaScritta(d).replace(',', '.')) * 1000
               : parseFloat(geo.distanzaScritta(d));
  assert.ok(n >= ultimo, `a ${d} m la distanza scritta è diminuita`);
  ultimo = n;
}

/* ------------------------- quanto manca, e a quale fermata */

/* La riga del GPS misura quello che c'è davanti: la prossima fermata e quella
   dove si scende. Prima misurava la più vicina, e appena passata una stazione
   la più vicina era quella — con la distanza che cresceva a ogni lettura. */

const gps = new Function(`
  let posizione = null, guardiaGPS = 1;
  const navigator = { geolocation: {} };
  const esc = (s) => String(s);
  const icona = () => '';
  const titolo = (s) => s;
  ${ritaglia('function metriFra', '/* Quanto dista una fermata')}
  ${ritaglia('/* Quanto dista una fermata', "/* Dov'è adesso")}
  ${ritaglia('function rigaPosizione', "/* L'età della posizione")}
  ${ritaglia('function etaPosizione', 'const MAPPA_Z')}
  return (d, dove, acceso = 1) => {
    posizione = dove; guardiaGPS = acceso;
    return rigaPosizione(d);
  };`)();

// Un viaggio con qualche fermata dietro e qualcuna davanti, e la posizione
// poco oltre l'ultima servita: è lì che la fermata più vicina era quella
// appena lasciata, e la misura cresceva invece di scendere.
const fermate = [
  { name: 'MILANO CENTRALE', lat: 45.4863, lon: 9.2049, passed: true },
  { name: 'LODI', lat: 45.3106, lon: 9.5033, passed: true },
  { name: 'PIACENZA', lat: 45.0503, lon: 9.6997, passed: true },
  { name: 'FIDENZA', lat: 44.8672, lon: 10.0680 },
  { name: 'PARMA', lat: 44.8060, lon: 10.3266 },
  { name: 'BOLOGNA CENTRALE', lat: 44.5057, lon: 11.3428 },
];
const scesi = (nome) => ({ stops: fermate.map((f) => (f.name === nome ? { ...f, chosen: true } : f)) });
const pocoOltre = { lat: 45.0400, lon: 9.7200, quando: Date.now() };

const aBologna = gps(scesi('BOLOGNA CENTRALE'), pocoOltre);
assert.match(aBologna, /ti mancano <b>[\d,]+ km<\/b> per FIDENZA/, 'la prossima è quella davanti');
assert.ok(!/PIACENZA/.test(aBologna), 'la stazione appena passata non è più la risposta');
assert.match(aBologna, /<b>[\d,]+ km<\/b> a BOLOGNA CENTRALE/, 'e accanto quanto manca all\'arrivo');

// Quando si scende alla prossima le due misure coincidono, e se ne scrive una
// sola: la riga che conta di più non deve dire due volte la stessa cosa.
const aFidenza = gps(scesi('FIDENZA'), pocoOltre);
assert.strictEqual((aFidenza.match(/km/g) || []).length, 1, 'una sola distanza');
assert.match(aFidenza, /per FIDENZA/, 'ed è quella della prossima');

// Senza fermata scelta l'arrivo è il capolinea, che è la destinazione scritta
// in cima alla scheda.
assert.match(gps({ stops: fermate }, pocoOltre), /a BOLOGNA CENTRALE/, 'senza scelta vale il capolinea');

// Le fermate senza coordinate si saltano invece di finire a zero gradi, che è
// nel golfo di Guinea: la prossima misurabile è quella dopo.
const senzaCoordinate = [
  { name: 'PIACENZA', lat: 45.0503, lon: 9.6997, passed: true },
  { name: 'FERMATA IGNOTA' },
  { name: 'FIDENZA', lat: 44.8672, lon: 10.0680 },
];
assert.match(gps({ stops: senzaCoordinate }, pocoOltre), /per FIDENZA/, 'la fermata senza posizione si salta');

// Treno arrivato: davanti non c'è più niente, e "mancano" sarebbe la parola
// sbagliata. Resta dove sei rispetto all'ultima.
const finito = gps({ stops: fermate.map((f) => ({ ...f, passed: true })) }, pocoOltre);
assert.match(finito, /sei a <b>[\d,]+ km<\/b> da BOLOGNA CENTRALE/, 'a viaggio finito cambia il verbo');

// A GPS spento c'è il bottone e non una misura: è quello che tiene ferma la
// richiesta di permesso all'apertura dell'app.
const spento = gps({ stops: fermate }, null, null);
assert.match(spento, /data-gps/, 'a GPS spento resta il bottone');
assert.ok(!/km/.test(spento), 'e nessuna distanza');

// La mappa sta dietro a un tab, chiuso: è lei a volere la posizione, e montata
// da sola chiedeva il permesso a chi aveva aperto l'app per guardare l'orario.
const tab = new Function(`
  let mappaAperta = false;
  const navigator = { geolocation: {} };
  const icona = () => '';
  ${ritaglia('function sezioneMappa', '// La mappa è montata')}
  return (d, aperta) => { mappaAperta = aperta; return sezioneMappa(d); };`)();

const conCoordinate = { stops: [{ name: 'PIACENZA', lat: 45.0503, lon: 9.6997 }] };
const chiuso = tab(conCoordinate, false);
assert.match(chiuso, /aria-expanded="false"/, 'il tab nasce chiuso');
assert.ok(!/posto-mappa/.test(chiuso), 'e la mappa non è in pagina');
assert.match(tab(conCoordinate, true), /posto-mappa/, 'aperto, il riquadro c\'è');

// Senza una fermata da segnare non c'è niente da aprire: sarebbe un tab su un
// riquadro vuoto.
assert.strictEqual(tab({ stops: [{ name: 'SENZA COORDINATE' }] }, false), '', 'niente coordinate, niente tab');

console.log('riga del GPS: ok — 9 casi su prossima fermata e arrivo + 4 sul tab della mappa');

const mercatore = new Function(`
  const MAPPA_Z = 13, TILE = 256;
  ${ritaglia('function proietta', '/* La mappa viva')}
  return proietta;`)();

const N = 256 * 2 ** 13;
// Il meridiano zero cade in mezzo al mondo, e così l'equatore.
assert.ok(Math.abs(mercatore(0, 0).x - N / 2) < 0.01, 'longitudine 0 al centro');
assert.ok(Math.abs(mercatore(0, 0).y - N / 2) < 0.01, 'latitudine 0 al centro');
// Verso est la x cresce, verso nord la y *scende*: è il verso dello schermo, e
// invertirlo è l'errore che ribalta la mappa sottosopra.
assert.ok(mercatore(45, 10).x > mercatore(45, 9).x, 'a est la x cresce');
assert.ok(mercatore(46, 9).y < mercatore(45, 9).y, 'a nord la y scende');
// Due stazioni vicine cadono in pixel vicini: la scala è quella giusta, non
// mille volte più grande o più piccola.
const a = mercatore(45.486347, 9.204528), b = mercatore(45.484917, 9.187683);
const px = Math.hypot(a.x - b.x, a.y - b.y);
assert.ok(px > 60 && px < 130, `Centrale-Garibaldi = ${Math.round(px)} px a zoom 13`);

console.log('mappa: ok — 11 casi su distanze e scrittura + 5 sulla proiezione');

/* ------------------------------- il viaggio di una scheda aperta */

/* La scheda aperta si rilegge col tabellone, e la sua risposta non torna mai
   indietro: era il modo in cui un "ViaggiaTreno non segue questo treno" —
   vero per il treno di fra tre ore, o per il mezzo minuto in cui quel servizio
   non aveva risposto — restava scritto sotto la scheda per sempre, con la
   pastiglia del ritardo misurato sulla riga sopra a smentirlo. */

const fabbricaViaggio = new Function('fetch', 'chiaveTabellone', `
  const stato = { da: 1, a: 2, arrivi: false };
  const viaggi = new Map();
  const disegna = () => {};
  const API = { treno: () => 'api/train' };
  ${ritaglia('/* Un viaggio che non ha niente da mostrare', '/* ------------------------------------------------------------------ eventi */')}
  return { viaggi, scaricaViaggio, viaggioVuoto };`);

// Il viaggio vero e quello che non ha niente da dire.
const conFermate = { tracked: true, stops: [{ scheduled: '19:12' }] };
const nonSeguito = { tracked: false };

// Una fabbrica per caso: le risposte si danno in fila, e `dove` è il tabellone
// che si sta guardando quando la risposta arriva.
function ambiente(risposte, dove = () => 'x') {
  let i = 0;
  return fabbricaViaggio(async () => {
    const r = risposte[i++];
    if (r === 'errore') throw new Error('rete');
    return { ok: true, json: async () => r };
  }, dove);
}

const casi = [
  ['il primo viaggio si scrive', [conFermate], 1, 'ok'],
  ['un treno non seguito si scrive', [nonSeguito], 1, 'non seguito'],
  // Il caso del bug: la seconda lettura si fa, e corregge la prima.
  ['una risposta vuota si rilegge', [nonSeguito, conFermate], 2, 'ok'],
  // E non si torna indietro, né per una risposta vuota né per la rete.
  ['un viaggio vero non si cancella', [conFermate, nonSeguito], 2, 'ok'],
  ['né lo cancella un errore di rete', [conFermate, 'errore'], 2, 'ok'],
  ['senza niente in mano l\'errore si dice', ['errore'], 1, 'errore'],
];

for (const [nome, risposte, letture, atteso] of casi) {
  const env = ambiente(risposte);
  (async () => {
    for (let i = 0; i < letture; i++) await env.scaricaViaggio('24562');
    const v = env.viaggi.get('24562');
    const esito = v.stato !== 'ok' ? v.stato
      : (env.viaggioVuoto(v) ? 'non seguito' : 'ok');
    assert.strictEqual(esito, atteso, nome);
  })().catch((e) => { console.error(e); process.exit(1); });
}

// Cambiato tabellone mentre la richiesta era in volo, la risposta si butta:
// `viaggi` è già stato svuotato per il tabellone nuovo, e quel treno lì non c'è.
{
  let dove = 'x';
  const env = fabbricaViaggio(async () => {
    dove = 'y';
    return { ok: true, json: async () => conFermate };
  }, () => dove);
  env.scaricaViaggio('24562').then(() => {
    assert.strictEqual(env.viaggi.get('24562').stato, 'attesa',
      'la risposta di un altro tabellone non si scrive');
  }).catch((e) => { console.error(e); process.exit(1); });
}

/* E la rilettura parte solo per i treni che il tabellone porta ancora: aperti
   tiene anche quelli partiti, e le loro richieste continuerebbero a partire
   ogni minuto per una scheda che non è più in pagina. */
const chieste = [];
new Function('scaricaViaggio', `
  const aperti = new Set(['24562', '24564']);
  const stato = { dati: { trains: [{ number: '24564' }] } };
  ${ritaglia('function rileggiSchedeAperte', '/* Le linee non bloccano')}
  return rileggiSchedeAperte;`)((n) => chieste.push(n))();
assert.deepStrictEqual(chieste, ['24564'], 'si rilegge solo la scheda ancora in lista');

console.log('viaggio della scheda: ok — 6 casi sulla rilettura + 2');
