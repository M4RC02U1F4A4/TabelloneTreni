# Tabellone Treni

Una versione per telefono dei tabelloni di [RFI](https://iechub.rfi.it/ArriviPartenze),
con in più la cosa che al sito originale manca: **il filtro per dove devi
andare**. Scegli partenza e arrivo e vedi solo i treni che fermano davvero lì,
con l'orario a cui ci arrivano.

- **due ritardi per treno**: quello del tabellone RFI e quello misurato sul treno da ViaggiaTreno, che non dicono la stessa cosa
- **il binario cambiato si vede**, e si vede da quale binario il treno si è spostato
- **toccando un treno si vede dov'è adesso**, con gli orari reali delle fermate che ha già servito
- **lo stato delle linee Trenord**, con il testo degli avvisi e la **notifica sul telefono** quando cambia il bollino di una linea seguita — scioperi compresi
- **gli avvisi di stazione in cima alla home**, la striscia gialla che RFI fa scorrere in fondo al tabellone: ascensori guasti, lavori che spostano i treni per mesi. Toccandola si apre e si legge per intero
- **i treni cancellati restano nella tratta**, con la scritta *soppresso*, anche quando RFI non pubblica le loro fermate — che è sempre, ed è il giorno di sciopero il giorno in cui serve saperlo
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

#### Cosa si vede

In home la sezione **non elenca tutte e 65 le linee**: mostra quelle seguite e
quelle che in questo momento hanno un problema. In una giornata normale sono
zero righe, ed è l'informazione giusta — la lista intera sta dietro a "Tutte",
raggruppata come la raggruppa Trenord.

La campanella marca le linee che ti riguardano: le tiene in cima alla home ed è
quello che decide chi riceve le notifiche. Si salva il **codice** della linea,
non il nome, che cambia quando cambia un capolinea.

In cima all'elenco completo c'è un campo che filtra su nome e codice. Il nome di
una linea è la catena delle sue stazioni — "Saronno-Milano Passante-Lodi" —
quindi cercare una stazione funziona senza indicizzarle a parte, e i gruppi
rimasti vuoti spariscono invece di lasciare intestazioni sopra il niente. Il
campo sta fuori dal pezzo che si ridisegna: filtrando si riscrive solo l'elenco,
e quello che si sta scrivendo resta al suo posto con il cursore dov'era.

Accanto al bollino c'è sempre la parola che lo traduce — "regolare",
"criticità", "gravi criticità". Il colore da solo non è un'informazione per
tutti, e tre pallini muti si distinguono male in mezzo a sessantacinque righe.

I bollini non fanno mai aspettare il resto: la home compare subito e la sezione
si riempie quando i dati arrivano. Se il servizio non risponde, al suo posto
c'è una riga sottovoce — che manchino i semafori non deve sembrare che sia
rotto il tabellone, che è l'unica cosa per cui l'app si apre di corsa.

#### Le notifiche

Accendere una campanella chiede il permesso e registra un abbonamento Web Push.
Quando un bollino cambia, chi segue quella linea riceve la notifica **anche con
l'app chiusa**: è il motivo per cui la spedizione sta sul server e non nel
telefono — una PWA sospesa non esegue niente, e su iOS resta sospesa per giorni.

Il testo dice il **verso**, non solo lo stato d'arrivo: "circolazione
peggiorata", "tornata regolare". Sulla schermata di blocco si legge solo quella
riga, e "criticità" da sola non distingue una linea che peggiora da una che si
sta riprendendo.

Su iOS le notifiche web funzionano **solo con l'app aggiunta alla schermata
Home**: aperta come pagina in Safari, l'oggetto `Notification` non esiste
proprio. L'interfaccia lo dice invece di lasciare una campanella che sembra
funzionare e non suona mai — e in quel caso la campanella resta comunque utile,
perché tiene la linea in cima alla home.

Il permesso si chiede **dentro il tocco**: `Notification.requestPermission()`
parte nel gestore del click e la sua promessa si aspetta dopo, perché su iOS una
chiamata fatta dopo un `await` non conta più come gesto dell'utente.

#### Quando avvisarti: le fasce

Un guasto sulla linea con cui vai al lavoro è una notizia alle 7 e un ronzio
alle 15. Dall'orologio in cima all'elenco delle linee si scelgono le **fasce** in
cui le notifiche possono suonare — giorni e orari, quante ne servono: chi lavora
si mette andata e ritorno e per il resto della giornata non sente niente.

Senza nessuna fascia arrivano a qualunque ora, che è come stavano le cose prima:
chi non configura niente non si accorge del cambiamento.

**Fuori dalle fasce non si scarta, si rimanda.** Un guasto comparso alle 6 e
ancora in corso alle 7 arriva alle 7; uno comparso alle 6 e rientrato alle 6 e
mezza non arriva, perché alle 7 non c'è più niente da dire. Non c'è nessuna coda
di notifiche in attesa: il servizio sa com'è la linea *adesso* e sa cosa ti ha
già raccontato, e la differenza fra le due cose è tutto quello che serve. La
domanda non è "cos'è cambiato" ma "cosa non ti ho ancora detto".

Da qui una conseguenza sull'archivio: **la memoria di cosa è già stato detto sta
per abbonato**, non una volta per tutti. Con le fasce due persone non sono più
allo stesso punto della storia — chi ascolta la mattina e chi ascolta la sera
hanno sentito cose diverse — e un solo "ultimo stato noto" non potrebbe
rispondere a entrambi. Non arriva dal telefono: l'app riallinea l'abbonamento a
ogni avvio, e prenderla da lì la azzererebbe ogni volta.

Il **primo contatto** con una linea si prende comunque, anche a fascia chiusa: è
il punto di partenza della storia e non una notizia. È anche quello che rende
possibile il caso delle 6→7 — rimandandolo, la prima mattina utile il guasto
sembrerebbe il punto di partenza invece di una novità, e arriverebbe in silenzio.

Gli orari si leggono sul **quadrante del telefono**: l'app manda il proprio fuso
insieme alle fasce. Il database dei fusi è compilato dentro il binario
(`time/tzdata`) perché l'immagine è distroless static, dove `/usr/share/zoneinfo`
non è garantito: senza, d'estate le fasce si leggerebbero due ore sbagliate.

Gli abbonamenti stanno in un file JSON sul volume del servizio, riscritto per
intero a ogni modifica e con un rename atomico: sono decine, e un database qui
costerebbe più di quanto risolve, ma un file troncato a metà da un riavvio
perderebbe tutti gli abbonati insieme. Quando il servizio push risponde 404 o
410 l'abbonamento viene tolto: l'app è stata disinstallata o il permesso
revocato, e insistere è solo traffico.

**Le chiavi VAPID se le genera il servizio al primo avvio** e stanno sullo
stesso volume, in `chiavi.json` con permessi 600. Non c'è nessun segreto da
creare a mano, ed è voluto che stiano lì e non altrove: cambiare le chiavi rende
inservibili tutti gli abbonamenti presi, e perdere il volume li perde comunque.
Tenerle separate creerebbe l'unico caso davvero brutto — chiavi nuove e
abbonamenti vecchi — che è anche quello che nessuno noterebbe, perché fallisce
in silenzio a ogni invio.

La cifratura è quella di RFC 8291 con la firma VAPID di RFC 8292, e la fa
[webpush-go](https://github.com/SherClockHolmes/webpush-go). È l'unica
dipendenza aggiunta oltre a `golang.org/x/net`, ed è aggiunta apposta: ECDH più
HKDF più AES-GCM più un JWT ES256 non è codice da scrivere in casa per
risparmiare una riga in `go.mod`.

#### Perché il bollino non basta

Il semaforo dice che qualcosa non va, non cosa. Il testo sta sul dettaglio
della linea, a `/rest/render/line-details`, nella stessa forma dell'elenco: un
JSON che incarta un frammento HTML, con un blocco per comunicazione, ciascuno
con la propria data.

Il dettaglio pesa oltre 130 KB, quasi tutto elenco di stazioni, quindi non si
prende mai per tutte e 65 le linee. Si prende in due momenti, ed è la stessa
regola vista da due lati — **si paga solo quello che qualcuno guarda davvero**:

- **a ogni lettura, per le linee seguite da qualcuno.** Servono al servizio per
  accorgersi degli avvisi nuovi e mandare le notifiche. Nessun abbonato,
  nessuna richiesta.
- **a richiesta, quando si apre una riga.** Ogni linea si apre, anche quelle che
  non segue nessuno, e le comunicazioni arrivano al momento. Restano valide per
  un giro di lettura, e le richieste sulla stessa linea si mettono in fila
  dietro una sola: dieci persone che aprono la stessa riga insieme producono una
  lettura sola verso Trenord.

Una lettura non è appesa a chi l'ha chiesta: se il telefono rinuncia, o rinuncia
il tabellone che aspetta meno, quello che si è già letto finisce comunque in
cache e il tocco successivo è immediato invece di ricominciare da capo.

**Gli scioperi arrivano da qui**, e non da una fonte propria: Trenord li
pubblica come comunicazioni sulle linee interessate, giorni prima. Che siano
proprio le linee che segui è il punto — uno sciopero cambia la giornata solo su
quelle.

Un avviso nuovo produce una notifica. Il confronto è sul **testo** e non sulla
data: Trenord ripubblica lo stesso avviso con l'ora aggiornata quando lo
ritocca, e avvisare due volte della stessa cosa è il modo più rapido per far
spegnere le notifiche. Come per i bollini, la prima lettura dopo un avvio non
annuncia niente, altrimenti ogni rilascio riannuncerebbe i lavori annunciati ad
agosto.

Quando il bollino si muove e insieme arriva una comunicazione, la notifica è
**una sola**: sono la stessa cosa vista da due lati, e il testo dice il perché
accanto al cosa — "Circolazione peggiorata: criticità · Il treno 2528 viaggia
in ritardo per un guasto al sistema di chiusura delle porte".

#### La fonte risponde due cose diverse

**Lo stesso indirizzo, interrogato due volte di fila, risponde da backend che
non concordano.** Misurato: dieci richieste consecutive nel giro di secondi
danno due varianti a caso — sei volte una, quattro l'altra — che differiscono
su una dozzina di linee. Campionando ogni minuto si vedono quelle dodici linee
ribaltarsi tutte insieme, nello stesso istante, in direzioni opposte. Non è la
circolazione che cambia: dodici linee non guariscono allo stesso secondo.

Preso per buono così, un quarto delle linee sembra cambiare stato ogni pochi
minuti, e chi ha una campanella accesa riceve notifiche per movimenti che non
esistono.

L'elenco delle 65 linee non porta nessun orario, quindi non c'è modo di
riconoscere la risposta vecchia: resta la fonte dei pallini, dove sbagliare per
cinque minuti non fa danno. **Le notifiche nascono invece dal dettaglio della
linea, che un orario ce l'ha** — e fra le due varianti quella con l'orario più
recente è sempre risultata la giusta, anche sulla gravità:

| linea | variante vecchia | variante fresca |
| --- | --- | --- |
| S8 | criticità, 16:58 | regolare, 17:09 |
| S13 | regolare, 16:35 | criticità, 17:18 |
| R4 | criticità, 15:38 | **gravi** criticità, 17:31 |

Una risposta più vecchia dell'ultima vista viene quindi scartata, e indietro non
si torna. Un cambio di stato arriva così al più tardi alla lettura successiva,
invece di arrivare quattro volte e tre delle quali per sbaglio.

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

### Gli avvisi di stazione

In fondo alla pagina del monitor RFI fa scorrere una striscia gialla con gli
avvisi della stazione: `ASCENSORI BINARI 14/15 - 16/17 - 18/19 - 20 FUORI
SERVIZIO`, `DAL 14 GIUGNO AL 13 SETTEMBRE VARIAZIONI AI TRENI S11 ED S6 PER
LAVORI TRA RHO E MILANO CERTOSA`. È l'unico posto in cui quelle cose compaiono:
il tabellone dei treni non ne dice niente, e chi guarda solo quello lo scopre
dal cartello in stazione.

Sono nel markup senza nessun id, dentro un `div.marqueeinfosupp` — solo il nome
della classe con cui il CSS li anima — e **dipendono dal verso**: la stessa
stazione, alla stessa ora, annuncia i lavori sulle partenze e gli ascensori
sugli arrivi.

In home la striscia sta sopra ogni altra cosa, perché un ascensore fuori
servizio cambia il viaggio prima ancora della scelta del treno. Chiusa scorre,
che è il solo modo di far stare in una riga un testo lungo come un SMS;
toccandola si apre, si ferma e mostra tutto, con il nome della stazione sopra
ogni avviso — `ASCENSORI BINARI 14/15 FUORI SERVIZIO` senza sapere dove non è
un'informazione. Sotto `prefers-reduced-motion` non scorre affatto: resta ferma
e troncata, e per leggerla si apre.

Un avviso che vale per due stazioni compare **una volta sola**, con entrambi i
nomi nell'etichetta: un cantiere fra due fermate le stazioni lo pubblicano
tutt'e due, con lo stesso testo, e ripeterlo una volta per stazione occuperebbe
il doppio dello spazio per dire una cosa sola. Il raggruppamento è sul testo
esatto: due avvisi che dicono la stessa cosa con una parola diversa restano due
avvisi, perché non sta all'app decidere che siano lo stesso.

Gli avvisi si chiedono per le **stazioni di partenza dei preferiti**, distinte,
al massimo otto. Senza preferiti non c'è niente da chiedere e nessuna richiesta
parte. Hanno una cache propria da **dieci minuti**, più lunga di quella da
trenta secondi dei tabelloni: la home si aggiorna una volta al minuto e per ogni
preferito, e ogni buco costerebbe a RFI una pagina da 280 KB per una striscia
di testo che copre tre mesi. Una stazione che non risponde si salta in silenzio
— un banner giallo che dice che il banner giallo non funziona è peggio del
banner che manca.

### I treni di cui RFI non pubblica le fermate

Il filtro per destinazione tiene un treno se trova la stazione di arrivo fra le
sue fermate successive, quelle che RFI stampa nel popup della riga. Ma **quel
popup non c'è per tutti i treni**, e i treni cancellati non ce l'hanno mai: la
loro cella dei dettagli è letteralmente vuota. Con la tratta impostata
sparivano, e chi aspetta un treno cancellato non poteva distinguere «è
cancellato» da «non è in questa fascia oraria» — cioè non poteva sapere l'unica
cosa che in un giorno di sciopero conta.

Non è un problema dei cancellati: è un problema di **chi non ha l'elenco
fermate**. Campione preso a Milano Centrale in un giorno di sciopero, 22 treni
in partenza: 6 cancellati, e nessuno dei sei con le fermate. Ma senza fermate
c'era anche il 2975, non cancellato, con `RITARDO` scritto al posto dei minuti:
spariva dalla tratta pure lui.

Per quei treni le fermate si chiedono a **ViaggiaTreno**, e l'aggancio alla
destinazione è sul **codice stazione**, non sul nome: è un confronto esatto,
mentre il riconoscimento delle abbreviazioni deve indovinare, e su un treno che
non compare da nessun'altra parte conviene la strada che non indovina.
L'elenco di ViaggiaTreno parte dall'origine del treno, che di solito è prima
della stazione da cui lo si guarda, e va tagliato lì: senza il taglio la scheda
mostrerebbe fermate già passate, e la destinazione risulterebbe servita anche
da un treno che da lì è già transitato.

Sul treno cancellato le fermate restano **vuote**: la scheda non si apre, e non
c'è nessun viaggio da seguire. Sugli altri si riempiono, così la riga si apre
come tutte quelle di cui le fermate le ha pubblicate RFI.

Le liste hanno una cache **senza scadenza**, con il giorno di partenza nella
chiave: le fermate di un treno sono le stesse per tutta la giornata — a
differenza di dove si trova, che è il motivo per cui la cache dei viaggi vive
trenta secondi — e la voce di ieri non la cerca più nessuno. In cache va anche
**l'esito negativo**: in un giorno di sciopero ViaggiaTreno non conosce metà dei
treni cancellati, e senza ricordarselo si ripeterebbero venti richieste a vuoto
ogni mezzo minuto, per sempre.

A cache fredda sono una ventina di richieste a un servizio che ha otto secondi
di timeout, quindi partono insieme e **non si aspettano oltre due secondi e
mezzo**: i treni non ancora risolti compaiono al rinfresco dopo, dalla cache.
Una tratta quasi completa subito è più utile di una completa fra otto secondi,
che nessuno resta a guardare. Le richieste, però, non vengono annullate quando
si smette di aspettarle: finiscono di riempire la cache, che è quello che rende
utile il giro successivo.

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
| `DATI` | *(vuoto)* | cartella dove tenere abbonamenti e chiavi; vuoto significa solo in memoria, e si perdono a ogni riavvio |

| rotta | |
|---|---|
| `GET /linee` | stato di tutte le linee, con gli avvisi di quelle seguite e l'orario dell'ultima lettura riuscita |
| `GET /avvisi?linea=S2` | le comunicazioni di una linea, prese al momento se quelle che si hanno sono scadute |
| `GET /push/chiave` | la chiave pubblica VAPID; vuota se le notifiche non sono configurate |
| `POST /push/abbonamenti` | registra chi seguire, con le fasce e il fuso; un elenco di linee vuoto cancella l'abbonamento |
| `GET /healthz` | 503 finché non è riuscita una lettura: appena avviato non deve ricevere traffico |

Il volume va ceduto all'utente `nonroot` (uid 65532) la prima volta, perché
l'immagine non gira da root e un volume nuovo appartiene a root. In Kubernetes
lo fa `fsGroup`; con Docker serve un giro da un'altra immagine, visto che questa
è distroless e non ha una shell — il comando sta in `compose.yaml`.

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
- **Le notifiche su iOS vogliono l'app installata.** Web Push su iPhone
  funziona solo dalla schermata Home, non da una scheda di Safari. L'app lo
  dice, e lì la campanella serve solo a tenere la linea in cima.
- **I bollini coprono la sola Lombardia.** Sono le linee di Trenord: un treno
  RFI fuori regione non ha nessuno stato di linea associato.
- **Gli avvisi in home vengono dal solo tabellone partenze.** I due versi ne
  pubblicano di diversi, ma raddoppiare le pagine scaricate per una striscia non
  vale quello che si guadagna: un avviso che RFI mette solo sugli arrivi in home
  non si vede.
- **Sul treno sdoppiato il filtro può sbagliare una delle due righe.** Lo stesso
  numero compare due volte sul tabellone, con due destinazioni e due stati — a
  Milano Centrale il 25512, alle 09:43, per Chiasso e per Locarno — ma
  ViaggiaTreno di quel numero conosce un treno solo. Le due righe leggono quindi
  la stessa lettura. Le fermate risolte si riscrivono per riga e non per numero,
  così una non si prende il percorso dell'altra, ma quale delle due sia quella
  che ViaggiaTreno descrive non è deducibile dai dati disponibili. È la stessa
  ambiguità che c'è già sul ritardo.
- **Un treno cancellato che ViaggiaTreno non conosce resta fuori dalla tratta.**
  Le sue fermate non le pubblica nessuno, e tenerlo comunque vorrebbe dire
  mostrare in mezzo alla tratta dei treni che vanno da un'altra parte: in un
  giorno di sciopero, a una stazione grande, sarebbero venti cancellati per
  Roma e Torino in mezzo a cinque treni utili.

## Rilasci

Ogni commit su `main` fa una versione: `feat:` alza la minor, `fix:` e `perf:`
la patch, un `!` o un `BREAKING CHANGE:` la major, il resto vale patch. Il
workflow crea il tag (senza prefisso `v`), pubblica la release e spinge
l'immagine multi-architettura su GHCR.
