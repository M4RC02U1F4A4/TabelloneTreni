/* Il controllo di titolo(): `node ci/titolo.test.js`.
 *
 * Gira sul catalogo vero — tutte e 2435 le stazioni che il server incorpora —
 * perché i casi che rompono un title-case italiano non si inventano a tavolino:
 * sono le sigle di esercizio, le iniziali puntate e gli apostrofi che stanno
 * là dentro.
 *
 * Sta in ci/ e non accanto ad app.js perché main.go fa `//go:embed all:web`:
 * là dentro finirebbe nel binario, e verrebbe servito come una pagina. Il vincolo sulla lunghezza è quello che tiene in piedi evidenzia(),
 * che taglia il nome per posizione. */
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

// app.js è scritto per il browser: si carica il sorgente e si valuta il pezzo
// che serve, invece di aggiungere degli export che in produzione non servono.
const src = fs.readFileSync(path.join(__dirname, '..', 'web', 'app.js'), 'utf8');
const pezzo = src.slice(src.indexOf('const MINORI'), src.indexOf('/* Segna nel nome'));
const titolo = new Function(`${pezzo}; return titolo;`)();

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

console.log(`ok — 18 casi + ${nomi.length} stazioni del catalogo`);
