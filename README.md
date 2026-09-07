# Tabellone Treni

Una versione per telefono dei tabelloni di [RFI](https://iechub.rfi.it/ArriviPartenze),
con in più la cosa che al sito originale manca: **il filtro per dove devi
andare**. Scegli partenza e arrivo e vedi solo i treni che fermano davvero lì,
con l'orario a cui ci arrivano.

- **due ritardi per treno**: quello del tabellone RFI e quello misurato sul treno da ViaggiaTreno, che non dicono la stessa cosa
- **il binario cambiato si vede**, e si vede da quale binario il treno si è spostato
- **toccando un treno si vede dov'è adesso**, con gli orari reali delle fermate che ha già servito
- **segue il tema del telefono**, chiaro o scuro, senza un interruttore da toccare
- si aggiorna da solo una volta al minuto, e si ferma quando la pagina non è in primo piano
- le tratte si salvano fra i preferiti e stanno in cima alla home
- installabile sulla schermata iniziale del telefono
- immagine Docker da 16 MB con due servizi dentro, nessun database, nessuno stato su disco

|  |  |
|:--|:--|
| <img width="330" src="docs/home.png" alt="La home con quattro tratte fra i preferiti e le due voci per il tabellone di una stazione"> | <img width="330" src="docs/tratta.png" alt="I treni da Roma Termini che fermano a Roma Tiburtina, con il viaggio di un treno aperto"> |
| **Le tratte salvate stanno in cima**, e si aprono con un tocco; sotto, il tabellone intero di una stazione, partenze o arrivi. | **Solo i treni che fermano dove vai**. Toccando il treno si apre il suo viaggio: dov'è adesso, e a che ora è passato davvero dalle fermate già servite. |
| <img width="330" src="docs/tabellone.png" alt="Il tabellone completo delle partenze da Roma Termini, con i due ritardi affiancati su ogni treno"> | <img width="330" src="docs/ricerca.png" alt="La ricerca stazione con la corrispondenza evidenziata in mezzo al nome"> |
| **Il tabellone completo**: binari, soppressioni, avvisi e i **due ritardi** affiancati, ambra il tabellone e ciano la misura sul treno. | **La ricerca** cerca dentro il nome, non solo all'inizio, e non tocca la rete. |

## Come funziona

Il tabellone di RFI è HTML renderizzato dal server: non esiste un endpoint JSON
e il suo JavaScript non fa nemmeno una chiamata di rete. Il server qui dentro lo
scarica, lo interpreta e ne serve i dati.

Il motivo per cui il server esiste sono due cose che il browser non può
aggirare: RFI non manda intestazioni CORS, e la sua pagina pesa circa 280 KB che
**non comprime nemmeno se gliela si chiede compressa**. Ridotta a dati e
gzippata, diventa ~3 KB per un tabellone intero e ~400 byte per uno filtrato.

Il filtro per destinazione costa una sola richiesta per aggiornamento, perché
ogni partenza porta già con sé l'elenco completo delle fermate successive.

I tabelloni stanno in cache 30 secondi, presi sotto un lock per stazione: dieci
persone sulla stessa stazione producono comunque una richiesta sola verso RFI
ogni mezzo minuto. Se RFI smette di rispondere, per un minuto viene servito il
tabellone scaduto invece di un errore.

### I due ritardi

RFI pubblica il proprio ritardo con parsimonia, e sbaglia in due modi diversi.
Campione preso a Milano Porta Garibaldi alle 21:48: sui 20 treni presenti in
entrambe le fonti, ViaggiaTreno ne aveva sei con una misura vera, e su **cinque
di quei sei il tabellone diceva zero** mentre il treno viaggiava a +2, +2, +1,
−1, −1. Il sesto era un treno molto in ritardo: **RFI ne dichiarava 70, il treno
ne aveva 95**.

Sotto i pochi minuti il tabellone arrotonda a zero, e proprio lì si decide se il
treno si prende; sopra l'ora, si aggiorna con calma, e venticinque minuti di
differenza cambiano la sera.

Quale delle due letture sia quella giusta non lo decide l'app: le mostra
tutt'e due, una accanto all'altra, e il colore dice da dove viene il numero.
Che il treno sia in ritardo lo dice invece l'orario, che diventa rosso.

| Pastiglia | Campo JSON | Sorgente |
| --- | --- | --- |
| ambra | `delay` | la cella "ritardo" del tabellone RFI |
| ciano | `liveDelay` | `partenze`/`arrivi` di ViaggiaTreno, per la stessa stazione |

Le due fonti si accoppiano sul **numero del treno**, l'unico identificatore che
condividono. È anche una chiave prudente: lo stesso numero su due tabelloni
vicini è lo stesso treno, quindi un accoppiamento sbagliato fra stazioni non
trova niente invece di mostrare il ritardo di un altro treno.

**La pastiglia ciano manca finché il treno non è stato rilevato.** ViaggiaTreno
manda `ritardo: 0` anche per un treno che non è ancora partito: è il valore di
partenza del campo, non una misura. A separare i due casi è `compRitardo`, che
sul primo dice "non partito" e sul secondo "in orario" — e uno zero mostrato
come puntualità sarebbe una puntualità che nessuno ha visto. Un ritardo diverso
da zero, invece, vale come misura comunque, così la regola non si rompe il
giorno che compare una dicitura nuova.

La lettura di ViaggiaTreno è **facoltativa in ogni punto**: parte in parallelo a
quella di RFI (non in fila, altrimenti la pagina aspetterebbe la somma di due
servizi lenti), va nella stessa cache da 30 secondi, e se fallisce o se la
stazione non ha un codice ViaggiaTreno il tabellone esce come prima, con il solo
ritardo di RFI.

#### Le stazioni a due piani

Su questo le due fonti fanno il contrario l'una dell'altra, e va sistemato a
mano.

Il tabellone RFI della stazione "di sopra" è già la somma dei due livelli: a
Milano Porta Garibaldi porta quaranta treni, diciotto dei quali partono da un
binario `SOT`. ViaggiaTreno invece tiene due stazioni separate, `S01645` per la
superficie e `S01647` per il sotterraneo, e a chi chiede la prima risponde con i
soli treni di superficie.

Interrogando un codice solo, quindi, **tutti i treni del piano inferiore
restavano senza ritardo misurato e senza binario cambiato** — cioè le linee
suburbane, cioè quelle che prende più gente. Il difetto si vedeva solo nelle ore
in cui quei treni ci sono, il che spiega perché non era saltato fuori subito.

I due codici si interrogano insieme e le risposte si fondono. Il collegamento lo
fa il nome — la stazione `X` e la stazione `X SOTTERRANEA` — e non una lista
scritta a mano: sono due casi oggi (Milano Porta Garibaldi e Genova Piazza
Principe), ma il giorno che RFI ne aggiunge un terzo funziona da solo. Un
criterio più largo, "un nome che comincia per quest'altro", catturerebbe invece
`ALBA` e `ALBA ADRIATICA`, che sono due paesi diversi.

Se uno dei due livelli non risponde restano i treni dell'altro: mezzo tabellone
con i ritardi misurati è meglio di nessuno.

### Il binario cambiato

RFI pubblica una casella sola per il binario, e dal numero che c'è dentro non si
capisce se è quello di sempre o quello di stasera. ViaggiaTreno invece dichiara
i due valori separati — `binarioProgrammato` e `binarioEffettivo` — nella stessa
risposta che si scarica già per i ritardi: riconoscere un cambio non costa
nessuna richiesta in più.

Serve che ci siano **tutti e due**: con un valore solo non si sta confrontando
niente, e un "cambiato" annunciato per un campo mancante manderebbe qualcuno a
cercare un binario che non è cambiato affatto.

Quale dei due numeri sia la novità dipende da chi è avanti fra le due fonti, e
l'etichetta lo dice di conseguenza:

| Sul tabellone c'è | Etichetta | Perché |
| --- | --- | --- |
| il binario nuovo | `era 2` | serve il vecchio, per chi si è già incamminato |
| ancora il previsto | `ora 21` | serve il nuovo, che il tabellone non ha ancora preso |
| un terzo numero | `cambiato` | le fonti non concordano: si dice il fatto, non la direzione |

Il cambio non dipende dal ritardo: un treno non ancora rilevato non ha una
misura, ma può benissimo avere già un binario diverso da quello previsto — ed è
anzi il momento in cui la cosa serve di più, perché sei ancora sul piazzale a
decidere dove andare.

### Dov'è il treno adesso

Il tabellone dice di quanto un treno è in ritardo. Quando il numero è grosso la
domanda diventa un'altra — *ci arriva davvero?* — e la risposta è dove si trova
adesso. Toccando la scheda di un treno, sopra le sue fermate compare l'ultimo
punto in cui ViaggiaTreno l'ha visto, e le fermate già servite portano l'ora a
cui ci è passato davvero invece di quella prevista.

Costa una richiesta per treno su un servizio lento, quindi parte **solo quando
la scheda si apre**: farla per tutti e quaranta i treni di un tabellone
significherebbe pagarla quaranta volte per le due o tre schede che si aprono. I
viaggi già scaricati restano in mano al client, e lato server stanno in una
cache di trenta secondi, così toccare due volte la stessa scheda non chiede due
volte la stessa cosa.

Il treno si identifica con il tabellone da cui lo si è aperto: le coordinate che
ViaggiaTreno pretende — stazione di origine e giorno di partenza, oltre al
numero — le ha già lette il tabellone, e chiederle al client vorrebbe dire
fidarsi di quello che rimanda indietro.

**Non si prova a indovinare quando arriverà alle fermate che restano.**
ViaggiaTreno lì lascia zero, che è un campo non compilato e non una previsione,
e proiettare il ritardo attuale sugli orari futuri stamperebbe un'ora che
nessuno ha calcolato con l'aria di essere un dato.

La fermata dove scendi resta evidenziata anche in questa lista, e qui il
confronto avviene sul **codice stazione**, non sul nome o sull'orario: le due
fonti scrivono gli stessi posti in modi diversi, e un confronto sui nomi
sbaglierebbe proprio dove non si può sbagliare.

Gli orari li formatta il server in `Europe/Rome`, non il telefono: chi guarda
potrebbe essere altrove e vedrebbe orari spostati di un'ora accanto a quelli di
RFI, che italiani lo sono sempre. Il database dei fusi sta dentro il binario
(404 KB) perché l'immagine finale è una distroless static, dove non c'è nessun
`/usr/share/zoneinfo` su cui contare.

### Lo stato delle linee Trenord

RFI dice come va il singolo treno; se una linea intera ha un problema lo si
scopre treno per treno. Trenord pubblica il proprio semaforo per linea — le 65
linee lombarde con tre stati: regolare, con criticità, con gravi criticità — ed
è quel dato che viene raccolto qui.

**La pagina "Le nostre linee" arriva vuota.** L'elenco lo chiede il browser a
`/rest/render/shoulder-lines`, che risponde con un JSON contenente lo stesso
frammento HTML che la pagina si innesta da sé. Chiedere direttamente lì costa
135 KB invece del megabyte e passa della pagina completa, senza mappa Google né
widget di terze parti, e ne escono 5 KB di JSON per il telefono.

I tre stati e il fatto che siano tre non sono dedotti dai colori: la pagina
esegue uno script che si rilegge i propri semafori cercando le classi
`green-line`, `critical` e `danger`, ed è quella la fonte. Una linea il cui
semaforo non si riconosce viene **scartata**, non mostrata come regolare: se
Trenord cambia il markup è meglio una linea in meno che una falsa rassicurazione.

#### Perché è un servizio a parte

`statolinee` è un secondo processo, non un pezzo del tabellone, per tre motivi:

1. **Va interrogato una volta sola per tutti.** Il tabellone gira a due repliche;
   con il poller dentro, le letture verso Trenord raddoppierebbero insieme alle
   repliche, e più avanti raddoppierebbero anche le notifiche.
2. **Avrà stato su disco.** Gli abbonamenti alle notifiche vanno ricordati fra
   un rilascio e l'altro. Il tabellone non scrive niente e gira con il
   filesystem in sola lettura: è una proprietà che conviene non perdere.
3. **Se cade, cadono i bollini e basta.** I tabelloni sono la ragione per cui
   l'applicazione esiste e continuano a funzionare: `/api/lines` risponde 502 e
   il resto non se ne accorge.

I due binari stanno però nella **stessa immagine**, distinti dall'entrypoint:
vengono dallo stesso commit, si rilasciano insieme e non possono andare fuori
sincrono. La pipeline resta una.

Il tabellone fa da tramite su `/api/lines` invece di far parlare il telefono
direttamente con il servizio, per la stessa ragione per cui questo server
esiste: una sola origine, nessun CORS, nessun secondo indirizzo da conoscere.
L'ETag ci risparmia il corpo quando i bollini non cambiano, cioè quasi sempre —
li muove una persona in sala operativa.

Una lettura fallita **non azzera niente**: si continua a servire l'ultimo stato
buono, e il campo `updated` dice di quando è. Il primo giro dopo un avvio non
produce mai un cambio di stato, altrimenti ogni rilascio annuncerebbe come
"nuova" ogni linea che in quel momento non è regolare.

### Il riconoscimento delle fermate

I nomi delle fermate sul tabellone sono abbreviati e non combaciano con quelli
del catalogo: `MI BOVISA P.` sta per `MILANO BOVISA POLITECNICO`. Si risolvono
su due livelli:

1. i **nomi brevi di ViaggiaTreno**, uniti al catalogo una volta sola quando lo
   si genera, che coprono le contrazioni (`BOLOGNA C.LE` → `BOLOGNA CENTRALE`);
2. un confronto **token per token, per prefisso**, che copre i troncamenti
   (`GAZZADA SCHIAN.M` → `GAZZADA SCHIANNO MORAZZONE`).

Il confronto richiede lo stesso numero di token da entrambe le parti. È questo
vincolo a impedire che `LODI` catturi `LODI VECCHIO`: un falso positivo qui
farebbe salire qualcuno sul treno sbagliato, quindi la regola resta stretta e le
eccezioni vere si aggiungono a mano in `cmd/genstations/main.go`.

## Farlo girare

```sh
docker run -p 8080:8080 ghcr.io/m4rc02u1f4a4/tabellonetreni:latest
```

I servizi però sono due, quindi la forma completa è `docker compose up -d`:
oltre al tabellone parte `statolinee`, che è la stessa immagine avviata con
`entrypoint: ["/statolinee"]` e non pubblica nessuna porta — ci parla solo il
tabellone dalla rete interna.

In sviluppo bastano `go run .` e `go run ./cmd/statolinee` in due terminali;
l'interfaccia è HTML, CSS e JavaScript senza passo di build, quindi non serve
Node. Senza `statolinee` il tabellone funziona: è solo `/api/lines` a
rispondere 502.

Il tabellone:

| variabile | difetto | |
|---|---|---|
| `PORT` | `8080` | porta di ascolto |
| `ADDR` | `:8080` | indirizzo completo, ha la precedenza su `PORT` |
| `STATO_LINEE_URL` | `http://statolinee:8081` | dove risponde il servizio delle linee |

`statolinee`, che legge da Trenord ogni 5 minuti:

| variabile | difetto | |
|---|---|---|
| `PORT` | `8081` | porta di ascolto |
| `ADDR` | `:8081` | indirizzo completo, ha la precedenza su `PORT` |

| rotta | |
|---|---|
| `GET /linee` | stato di tutte le linee, con l'orario dell'ultima lettura riuscita |
| `GET /healthz` | 503 finché non è riuscita una lettura: appena avviato non deve ricevere traffico |

## Aggiornare il catalogo delle stazioni

Il catalogo (2435 stazioni con i loro alias e, per 2423 di esse, il codice
ViaggiaTreno da cui si leggono i ritardi) è committato in
`internal/stations/stations.json` ed embeddato nel binario: il server parte
istantaneamente e non dipende da due siti esterni per riuscire ad avviarsi.
Cambia molto di rado; per rigenerarlo:

```sh
go run ./cmd/genstations
```

## Limiti noti

- **Gli arrivi non si possono filtrare.** RFI pubblica le fermate successive
  solo sui tabelloni delle partenze. Chiedendo un filtro su un tabellone arrivi,
  l'app mostra tutti gli arrivi e lo dice.
- **Alcune stazioni non ci sono.** Il listino RFI non comprende la rete
  Ferrovienord (Saronno, Castellanza), quella svizzera oltre Chiasso, né
  impianti come Malpensa Aeroporto e Milano Bovisa Politecnico. Compaiono come
  fermate dei treni ma non sono selezionabili.
- **Le stazioni senza codice ViaggiaTreno restano al solo tabellone.** Sono
  dodici su 2435, quelle il cui nome non si accoppia con nessuna voce
  dell'elenco di ViaggiaTreno: lì la pastiglia ciano non compare mai.
- **Il markup di RFI può cambiare senza preavviso.** I test girano su pagine
  reali salvate in `internal/rfi/testdata`: se si rompono senza che sia cambiato
  il codice, è cambiato il sito. Vale lo stesso per Trenord, in
  `internal/trenord/testdata`.
- **Verso Trenord bisogna spacciarsi per un browser.** Con RFI l'applicazione si
  dichiara per quello che è; davanti a `trenord.it` c'è invece un Akamai Bot
  Manager che a uno User-Agent onesto risponde 403 su quell'endpoint. È l'unico
  header che conta — Referer e `X-Requested-With` non cambiano nulla, provati
  uno per uno — ed è isolato in una variabile sola, `trenord.UserAgent`.
- **I bollini coprono la sola Lombardia.** Sono le linee di Trenord: un treno
  RFI fuori regione non ha nessuno stato di linea associato.

## Rilasci

Ogni commit su `main` fa una versione: `feat:` alza la minor, `fix:` e `perf:`
la patch, un `!` o un `BREAKING CHANGE:` la major, il resto vale patch. Il
workflow crea il tag (senza prefisso `v`), pubblica la release e spinge
l'immagine multi-architettura su GHCR.
