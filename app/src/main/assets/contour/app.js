
(() => {
  const phone = document.getElementById("qwdtt")?.querySelector(".qd-phone");
  if (!phone) return;
  const logo = "data:image/png;base64,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";

  const artwork = `⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⢷⡀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣠⣤⣶⣶⣦⣤⣀⠀⠀⠀⠀⠀⠃⡌⢿⣿⣶⣦⣴⣾⣿⣶⡄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣴⣿⣿⣿⣻⣿⣿⣿⣿⡻⣦⣄⠀⠀⢸⣏⢒⠦⣭⣙⠽⣛⢿⣿⣿⣄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⡀⣴⣿⡿⣽⣛⡿⣿⣿⣿⣿⣿⣿⣤⢯⣗⠄⣿⢎⡜⢢⠕⣊⢵⠲⠌⢉⢿⣿⠀⠀⠀⠀⠀⠀⠀⢀⣀⣀⣀⣀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠸⣻⢿⠛⢿⣿⢻⣿⣿⡿⣟⢿⣿⣿⣿⣿⡤⣟⠤⣜⠼⣀⠇⠀⠀⣿⡟⠀⢘⣿⣿⠀⠀⣀⣠⣤⣼⣿⣿⣿⣿⣿⣿⣿⣄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠌⢃⠢⡻⢞⡤⠱⠙⢮⢯⢿⣿⣿⣷⢫⣖⠁⠜⠀⢠⣿⡟⣔⣿⣿⣿⣿⣿⣟⣿⣿⣿⣿⣿⣿⣿⣿⣿⠻⣿⡀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢣⣹⡌⣇⠷⣙⢲⡌⣑⠝⣿⣿⣿⡣⢞⣧⠘⢣⣼⣿⡽⣼⣿⣿⣿⣿⢿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⡝⢵⣧
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⢷⣿⣿⣽⣎⢧⡙⠖⢮⢢⢻⣿⣟⡜⣞⠀⢸⣿⣿⣳⢟⢿⣿⣿⠃⢸⡿⣿⣿⣿⣿⣿⣿⢿⣛⢿⣭⣳⡷⡀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠻⣽⣿⣿⣦⡛⠮⡃⢊⠣⡟⣟⡼⣩⡄⣿⣿⣿⢁⣥⠏⠑⢛⣠⡿⠃⣸⣿⣿⣿⣿⣮⣷⣻⡿⢿⣿⢿⣷⣶⡄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠘⢿⢿⣿⣿⣯⣹⠺⣄⠹⡙⣶⢳⣩⡿⠧⠏⣩⣥⣀⣼⣿⣿⣁⣸⣿⣿⣿⣿⣿⣿⣳⡿⣽⣣⢼⣫⠟⠋
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡀⢄⣢⢿⡛⣻⡿⣧⡿⣿⣿⣷⣏⡞⡲⡸⣘⣧⡟⡇⠉⡢⢫⠟⣻⡿⣛⣾⣻⣿⣿⣿⣿⣿⣿⣿⣿⣿⡷⠟⠋
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣶⣿⣿⣟⣎⣵⣾⣿⣿⣯⣞⡩⣻⢿⢎⣓⡵⢂⠙⠌⠁⠠⢃⡤⢛⣽⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⠟⠋
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣴⣿⣿⣿⣿⣯⢛⣫⡵⣾⣟⣾⣿⣿⣶⡯⣠⣶⣿⣶⣦⠀⠀⠀⠂⢖⢯⣯⣿⣿⣿⣿⣿⣿⣿⣿⣻⣿⡿⣻⣖⣦⣄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⣿⡿⣯⢗⡻⢭⣺⠽⡗⣯⣟⣼⣿⣿⣿⣿⣿⣆⡀⠀⡴⣻⣿⣿⣷⣝⣿⣯⣿⣿⣿⣷⣾⣯⡵⣾⣿⡻⣿⣷⣄
⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⣿⣿⣿⣿⣿⣳⢯⣻⣽⣿⣿⣿⣿⣿⢹⣿⣿⣿⣿⣿⣷⣾⣮⣷⣿⣿⣿⣿⣿⡝⣽⡿⣻⣿⡿⣟⡿⣝⡮⣳⣙⢿⣿⣧
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠻⡿⣿⣿⣿⣿⣿⡿⠿⠛⠛⠛⠛⠋⠉⠉⢿⣿⣿⣾⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣽⣿⣿⣿⣟⣮⢵⢎⡝⢮⣮⣻⣿⣿⡆
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠻⡿⣍⡱⠀⠀⠀⠀⠀⠀⠀⠀⣰⣤⡌⢻⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣟⣿⣿⠼⢿⣿⣿⣿⣾⣷⡪⡍⣣⠕⡵⣸⣿⡇
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠮⠝⠜⠁⠀⠀⠀⠀⠀⠀⠀⢰⠻⠟⠋⠀⢏⡱⠲⣍⠿⣿⠿⣿⣿⣿⣿⣿⣿⠏⠀⠀⠀⠀⠉⠉⠛⢹⠻⣄⢯⢪⣿⠏
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡊⠔⡱⢌⣒⣯⠿⣿⣻⣿⡿⠛⠁⠀⠀⠀⠀⠀⠀⠀⠀⠈⢶⣯⣼⠁
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢡⠍⣲⢣⢾⣡⢟⣞⣿⠟⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠛⠙⠃
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠐⢪⡜⣧⠓⣮⡟⣾⡏
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⢢⢽⣎⣟⡾⣽⡟
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠜⡜⢯⡿⠏`;
  const artworkRows = artwork.split("\n");
  const artworkInset = Math.min(...artworkRows.map((row) => row.match(/^⠀*/)[0].length));
  const artworkWidth = Math.max(...artworkRows.map((row) => row.length)) - artworkInset;
  // Braille dots 1–6 fill the first three rows by column. Dots 7 and 8 share the bottom row.
  const brailleDots = [[0, 0], [0, 1], [0, 2], [1, 0], [1, 1], [1, 2], [0, 3], [1, 3]];
  const artworkDots = new Path2D();
  const dotPitch = 4;
  for (const [rowIndex, row] of artworkRows.entries()) {
    for (const [columnIndex, cell] of Array.from(row.slice(artworkInset)).entries()) {
      const dots = cell.charCodeAt(0) - 0x2800;
      for (const [bit, [x, y]] of brailleDots.entries()) {
        if (dots & (1 << bit)) {
          artworkDots.rect((columnIndex * 2 + x) * dotPitch, (rowIndex * 4 + y) * dotPitch, 3.5, 3.5);
        }
      }
    }
  }

  function paintArtwork() {
    const canvas = phone.querySelector(".qd-latency-art");
    if (!canvas) return;
    const context = canvas.getContext("2d");
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = getComputedStyle(canvas).color;
    context.fill(artworkDots);
  }

  const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
  const galaxyRows = Array.from({ length: 12 }, (_, row) => Array.from({ length: 51 }, (_, col) => {
    const x = (col - 25) / 21;
    const y = (row - 5.5) / 4.35;
    const radius = Math.hypot(x, y);
    const seed = Math.sin(39.73 * (row + 2) + 17.19 * (col + 3)) * 1000;
    return {
      radius, angle: Math.atan2(y, x), grain: seed - Math.floor(seed),
      core: Math.exp(-radius * radius * 52), outer: Math.max(0, 1 - radius / 1.12),
    };
  }));
  const galaxySymbols = " .,:;=+*#@";
  const stillAngle = -0.56;
  let galaxyTimer;
  let galaxyAngle = stillAngle;

  function showGalaxy() {
    clearInterval(galaxyTimer);
    const field = phone.querySelector(".qd-galaxy");
    if (!field) return;
    const connecting = state.link === "starting";
    const spinning = (connecting || state.link === "up") && !reducedMotion.matches;
    function draw(rotation) {
      field.textContent = galaxyRows.map((row) => row.map(({ radius, angle, grain, core, outer }) => {
        const arm = (1 + Math.cos((angle - rotation) * 3 + radius * 8.4)) / 2;
        const trail = Math.pow(arm, 3) * Math.pow(outer, .75) * (.63 + grain * .68);
        const light = Math.max(grain > .967 ? .14 : 0, core * 1.13 + trail * .93);
        return galaxySymbols[Math.min(galaxySymbols.length - 1, Math.floor(light * 10))];
      }).join("")).join("\n");
    }
    if (!spinning) galaxyAngle = stillAngle;
    draw(galaxyAngle);
    if (spinning) {
      const rotationStep = connecting ? .015 : .045;
      galaxyTimer = setInterval(() => {
        galaxyAngle = (galaxyAngle + rotationStep) % (2 * Math.PI);
        draw(galaxyAngle);
      }, 75);
    }
  }

  const esc = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;" })[c]);
  const tabs = [["home","[+]","Туннель"],["servers","[::]","Серверы"],["profiles","[=]","Профили"],["bypass","[<>]","Обход"],["logs","[>_]","Логи"]];
  const modes = { vpn: "WireGuard", rawtun: "Raw", socks: "SOCKS5" };
  const dnsGroups = [
    ["UDP · порт 53", [["yandex","Яндекс DNS","77.88.8.8 · 77.88.8.1"],["cloudflare","Cloudflare","1.1.1.1 · 1.0.0.1"],["google","Google DNS","8.8.8.8 · 8.8.4.4"],["custom","Свой DNS","IP-адреса"]]],
    ["DoH · HTTPS", [["doh-yandex","Яндекс DoH","common.dot.dns.yandex.net"],["doh-cloudflare","Cloudflare DoH","cloudflare-dns.com"],["doh-google","Google DoH","dns.google"],["doh-custom","Свой DoH","URL сервера"]]],
  ];
  const dnsLabels = Object.fromEntries(dnsGroups.flatMap(([,items]) => items.map(([key,name]) => [key,name])));
  const state = {
    page: "home", link: "down", section: "", mode: "vpn", profiles: [], servers: [], groups: [], subscriptions: [],
    active: "", role: "user", lang: "ru", dns: { key: "yandex" }, phases: [], logs: [], workers: 0, session: 0,
  };
  const t = text => {
    const english = window.qwdttEnglish;
    return state.lang === "en" && english && Object.prototype.hasOwnProperty.call(english,text) ? english[text] : text;
  };
  const tunnelError = () => state.timeout
    ? t("Этап «{step}» не завершился за {seconds} с").replace("{step}",t(state.failed)).replace("{seconds}",state.timeout)
    : t(state.error || "");
  let draft, ports, socksSettings, dns, hashes, exported, importText = "", server, access, passwordEdit, routeApps = [];
  let tunnelPing = {}, profilePing = "", hashProbe, hashStatuses = {}, countryQuery = "";
  let picker = false, dnsList = false, activeGroup = "", busy = false, toastTimer;
  const pages = [];
  const panels = [];
  const pending = new Map();
  let sequence = 0;
  function native(action, data = {}, onRequest) {
    if (!window.ContourNative) return Promise.reject(new Error("Android не подключён"));
    const id = ++sequence;
    onRequest?.(id);
    return new Promise((resolve,reject) => {
      const timeout = setTimeout(() => {
        pending.delete(id);
        reject(new Error("Действие не завершилось вовремя"));
      }, action === "hash.check" ? 420000 : 120000);
      pending.set(id,{resolve,reject,timeout});
      window.ContourNative.postMessage(JSON.stringify({id,action,data}));
    });
  }
  window.qwdttResponse = response => {
    const request = pending.get(response.id);
    if (!request) return;
    pending.delete(response.id);
    clearTimeout(request.timeout);
    if (response.error) request.reject(new Error(response.error));
    else request.resolve(response.result);
  };
  window.qwdttOpen = section => {
    if (section === "settings" || section === "logs") go(section);
    if (section === "profiles" || section === "bypass") { go(section); refresh().catch(showError); }
  };
  function showError(error) {
    phone.querySelector(".qd-toast")?.remove();
    const node = document.createElement("p");
    node.className = "qd-toast";
    node.setAttribute("role","status");
    node.textContent = t(error.message || String(error));
    phone.append(node);
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => node.remove(), 5000);
  }
  window.qwdttMessage = showError;
  async function refresh() {
    Object.assign(state, await native("snapshot"));
    render();
  }
  const current = () => state.profiles.find(p => p.id === state.active);
  const lock = () => state.link === "up" || state.link === "starting";
  const btn = (action,label,text=label) => `<button class="cursor-interaction" type="button" data-act="${esc(action)}" aria-label="${esc(t(label))}">${esc(t(text))}</button>`;
  const row = (label,value,meta="") => `<div class="qd-row"><span>${esc(label)}${meta ? `<small>${esc(meta)}</small>` : ""}</span><span class="qd-mono qd-value">${esc(value)}</span></div>`;
  const link = (action,label,value=">",meta="") => `<button class="qd-row cursor-interaction" type="button" data-act="${esc(action)}"><span>${esc(t(label))}${meta ? `<small>${esc(t(meta))}</small>` : ""}</span><span class="qd-mono qd-value">${esc(t(value))}</span></button>`;
  const choice = (action,label,selected,disabled=false,meta="") => `<button type="button" class="cursor-interaction" data-act="${esc(action)}" aria-pressed="${selected}" ${disabled ? "disabled" : ""}>${esc(t(label))}${meta ? `<small>${esc(t(meta))}</small>` : ""}</button>`;
  const sw = (action,label,checked) => `<button type="button" class="qd-row qd-switch cursor-interaction" data-act="${esc(action)}" role="switch" aria-checked="${Boolean(checked)}"><span>${esc(t(label))}</span><span class="qd-switch-track" aria-hidden="true"></span></button>`;
  const input = (name,label,value,attributes="") => `<label class="qd-field"><span>${esc(t(label))}</span><input name="${name}" value="${esc(value)}" ${attributes}></label>`;
  const submit = (label="Сохранить") => `<button class="qd-wide-toggle cursor-interaction" type="submit">${esc(t(label))}</button>`;
  const form = (name,content) => `<form class="qd-form" data-form="${name}">${content}</form>`;
  const head = (title,action="") => `<div class="qd-section-head"><h2>${esc(t(title))}</h2>${action}</div>`;
  const section = (title,content) => `<section class="qd-block qd-network qd-dialog" aria-label="${esc(t(title))}"><div class="qd-section-head"><h3>${esc(t(title))}</h3>${btn("back","Закрыть","[x]")}</div>${content}</section>`;
  const group = (title,choices) => `<fieldset class="qd-setting"><legend>${esc(t(title))}</legend><div class="qd-segments">${choices}</div></fieldset>`;
  const port = (name,label,value) => input(name,label,value,'required type="number" inputmode="numeric" min="1" max="65535"');
  const secretInput = (name,label,value="") => input(name,label,value,'type="password" autocomplete="off"');
  const flag = code => /^[A-Z]{2}$/.test(code || "") ? `<img class="qd-flag" src="flags/${code.toLowerCase()}.png" alt="" loading="lazy" draggable="false">` : "";
  const profileTitle = p => `<span class="qd-name">${flag(p.flag)}<span>${esc(p.name)}</span></span>`;
  const dots = '<span class="qd-dots" aria-hidden="true"><i></i><i></i><i></i></span>';
  const labels = { up:"Активен", down:"Отключён", starting:"Подключение", failed:"Ошибка" };
  function stages() {
    const active = state.link !== "down";
    const allowed = active && state.phases.length ? state.phases : ["DNS","VK","WRAP","TURN",...(state.mode === "rawtun" || state.noDtls ? [] : ["DTLS"]),"Потоки",{vpn:"VPN", rawtun:"Raw", socks:"SOCKS"}[state.mode]];
    return `<div class="qd-stages ${allowed.length>=7 ? "qd-seven" : ""}" aria-label="${t("Этапы подключения")}" data-columns="${allowed.length}">${allowed.map(label => {
      const done = active && (state.completed || []).includes(label);
      const failed = active && label === state.failed, now = active && label === state.phase;
      const color = failed ? "qd-failed" : done ? "qd-online" : now ? "qd-current" : "";
      return `<div class="qd-step ${color}"><span aria-hidden="true">${failed?"[x]":done?"[+]":now?"[~]":"[ ]"}</span><span class="qd-phase-label">${esc(t(label))}</span></div>`;
    }).join("")}</div>`;
  }
  function hero() {
    return `<div class="qd-hero" data-link="${state.link}"><pre class="qd-galaxy" aria-hidden="true"></pre><h2 aria-live="polite">${t(labels[state.link])}</h2></div>`;
  }
  function tunnelAction() {
    const label = state.link === "up" ? "Остановить" : state.link === "starting" ? "Отменить" : state.link === "failed" ? "Повторить" : "Подключить";
    const glyph = state.link === "up" ? "[#]" : state.link === "starting" ? "[x]" : "[>]";
    return `<button class="qd-wide-toggle cursor-interaction" data-act="toggle"><span class="qd-glyph" aria-hidden="true">${esc(glyph)}</span>${t(label)}</button>`;
  }
  function home() {
    return hero() + `<div data-stages>${stages()}</div><div data-toggle>${tunnelAction()}</div>
      <p class="qd-error" data-error>${esc(tunnelError())}</p>
      <section class="qd-latency" aria-labelledby="qd-latency-label">
        <div class="qd-latency-main">
          <div class="qd-metric-stack">
            <div><p class="qd-metric-label">${t("АКТИВНЫХ")}</p><strong data-workers>${state.workers}</strong></div>
            <button class="qd-ping cursor-interaction" type="button" data-act="tunnel-ping" aria-label="${t("Проверить пинг")}">
              <span class="qd-metric-label" id="qd-latency-label">${t("Пинг")}</span><strong data-latency role="status"></strong>
            </button>
          </div>
          <canvas class="qd-latency-art" width="${artworkWidth * 2 * dotPitch}" height="${artworkRows.length * 4 * dotPitch}" aria-hidden="true"></canvas>
        </div>
        <div class="qd-latency-meta qd-mono"><span>${t("Протокол")}</span><button class="qd-mode-trigger cursor-interaction" data-act="mode" aria-label="${t("Протокол")} ${modes[state.mode]}"><span class="qd-mode-light">[ ${modes[state.mode]} ]</span><span aria-hidden="true">&gt;</span></button></div>
      </section>`;
  }
  function paintPing() {
    const node = phone.querySelector("[data-latency]");
    if (!node) return;
    const value = tunnelPing.pending ? `${dots}<span class="qd-sr">${t("Измерение пинга")}</span>`
      : tunnelPing.value != null ? `${tunnelPing.value} <span>${t("мс")}</span>`
      : tunnelPing.failed ? `<span class="qd-ping-failed">${t("Нет ответа")}</span>` : "—";
    if (node.innerHTML !== value) node.innerHTML = value;
    const action = node.closest("button");
    action.disabled = state.link !== "up" || !!tunnelPing.pending;
    action.setAttribute("aria-busy", Boolean(tunnelPing.pending));
  }
  async function measureTunnelPing() {
    if (state.link !== "up" || tunnelPing.pending) return;
    const measurement = tunnelPing = {pending: true};
    paintPing();
    try {
      const result = await native("tunnel.ping");
      measurement.value = Number.isFinite(result) && result > 0 ? result : null;
      measurement.failed = measurement.value === null;
    } catch {
      measurement.failed = true;
    } finally {
      measurement.pending = false;
      if (measurement === tunnelPing) paintPing();
    }
  }
  function logEntries() {
    const logs = state.logs || [];
    return `<p class="qd-log-counter">${t("Активных подключений")} <b class="qd-accent" data-workers>${state.workers}</b></p>
      ${state.stats ? `<p class="qd-note">${esc(state.stats)}</p>` : ""}
      ${logs.map(entry => `<div class="qd-log"><span class="${entry.error ? "qd-error" : "qd-mute"}">${entry.count > 1 ? "x" + Number(entry.count) : "[>]"}</span><span>${esc(entry.text)}</span></div>`).join("") || `<p class="qd-note">${t("Логи пусты")}</p>`}`;
  }
  function profilePage() {
    const items = state.profiles.filter(p => !activeGroup || p.groupId === activeGroup);
    return head("Профили",`<div class="qd-header-actions">
      ${items.length ? `<button class="qd-ping-action" data-act="ping" ${profilePing || lock() ? "disabled" : ""}>${t("Проверить пинг")}</button>` : ""}
      ${btn("add","Добавить профиль","[+]")}</div>`) +
      (state.groups.length ? `<div class="qd-pills">${choice("group:","Все",!activeGroup)}${state.groups.map(g=>choice("group:"+g.id,g.name,activeGroup===g.id)).join("")}</div>` : "") +
      `<div class="qd-block">${items.map(p => `<div class="qd-profile-line">
        <button class="qd-select-profile cursor-interaction" data-act="profile:${esc(p.id)}" aria-pressed="${state.active===p.id}"><span class="qd-mono qd-accent" aria-hidden="true">${state.active===p.id ? "[+]" : "[ ]"}</span><span class="qd-profile-name">${profileTitle(p)}${state.active===p.id ? `<small>${t("Активный профиль")}</small>` : ""}</span></button>
        <span class="qd-profile-ping qd-mono" role="status" aria-busy="${profilePing===p.id}">${profilePing===p.id ? dots+`<span class="qd-sr">${t("Измерение пинга")}</span>` : p.ping!=null ? Number(p.ping)+" "+t("мс") : ""}</span>
        <button class="qd-edit-star cursor-interaction" data-act="edit:${esc(p.id)}" aria-label="${t("Настроить профиль")} ${esc(p.name)}">*</button></div>`).join("") || `<p class="qd-note">${t("Добавьте или импортируйте профиль")}</p>`}</div>`;
  }
  function settingsPage() {
    return head("Настройки")+`<div class="qd-block">
      ${link("dns","DNS для VK",">",dnsLabels[state.dns?.key] || state.dns?.key)}
      ${sw("bool:verbose","Подробные логи",state.verbose)}
      ${sw("bool:wifi","Отключать на Wi‑Fi",state.wifi)}
      </div><div class="qd-section">${group("Язык",choice("lang:ru","Русский",state.lang==="ru")+choice("lang:en","English",state.lang==="en"))}
      ${group("Режим приложения",choice("role:user","Пользователь",state.role==="user")+choice("role:admin","Админ",state.role==="admin"))}</div>
      <details><summary>${t("Дополнительно")}</summary><div class="qd-block">
        ${sw("bool:autoRoutes","Автообновление обхода",state.autoRoutes)}
        ${link("update","Проверить обновления",state.version || ">")}
        ${link("notifications","Уведомления Android",">")}
      </div></details><div class="qd-block">${link("github","Проект на GitHub",">","konchul/proxy-turn-vk-android")}</div>
      <p class="qd-note qd-credits">Apple Color Emoji © Apple Inc. · EmojiTwo (EmojiOne 2.2, Ranks.com, CC BY 4.0)</p>`;
  }
  function bypassPage() {
    const mode = state.whitelist ? "Через туннель идут выбранные приложения и адреса." : "Выбранные приложения и адреса идут мимо туннеля.";
    return head("Обход") + `<div class="qd-block qd-bypass">
      ${group("Правила маршрутизации",choice("route-mode:0","Чёрный список",!state.whitelist)+choice("route-mode:1","Белый список",state.whitelist))}
      <p class="qd-note">${t(mode)}</p>${link("apps","Приложения")}
      ${form("routes",`<label class="qd-field"><span>${t("Сайты и IP")}</span><textarea name="routes" autocomplete="off" placeholder="${t("Домен или IP с новой строки")}">${esc(state.routes)}</textarea></label>${submit()}`)}
      <div class="qd-side-actions">${btn("bypass-import","Импорт")}${btn("bypass-export","Экспорт")}</div></div>`;
  }
  function serversPage() {
    return head("Серверы",btn("server.new","Добавить сервер","[+]")) + `<div class="qd-block">${state.servers.map(s => link("server.edit:"+s.id,s.name,">",s.host)).join("") || `<p class="qd-note">${t("Добавьте VPS")}</p>`}</div>`;
  }
  function networkPanel() {
    const locked = lock();
    return section("Сетевые настройки",
      group("Режим подключения",Object.entries(modes).map(([key,label])=>choice("value:mode:"+key,label,state.mode===key,locked)).join("")) +
      (state.mode === "rawtun" ? group("Шифрование Raw",choice("value:aes:0","ChaCha20",!state.aes,locked)+choice("value:aes:1","AES-256",state.aes,locked)) : "") +
      group("TURN-транспорт",choice("value:tcp:0","UDP",!state.tcp,locked)+choice("value:tcp:1","TCP",state.tcp,locked)) +
      group("Маскировка трафика",choice("value:mask:audio","Аудио",state.mask==="audio",locked,"OPUS")+choice("value:mask:video","Видео",state.mask==="video",locked,"H.264")) +
      (state.mode === "socks" ? link("socks","Настройки SOCKS5",">") : "") +
      link("ports","Порты") +
      (current() ? link("edit:"+state.active,"Параметры профиля",">") : "") +
      (locked ? `<p class="qd-note">${t("Настройки доступны после остановки туннеля.")}</p>` + btn("toggle","Остановить туннель") : ""));
  }
  const slots = values => Array.from({length:4},(_,index)=>values?.[index] || "");
  const hashCount = () => draft.hashes.filter(h => h.trim()).length;
  function profilePanel() {
    const max = hashCount()*27, value = max ? Math.max(9,Math.min(Math.round(draft.workers/9)*9,max)) : 0;
    return section("Настройки профиля",form("profile",
      input("name","Название",draft.name,'required maxlength="80"') +
      input("peer","IP или домен",draft.peer,'required spellcheck="false"') +
      `<button class="qd-row qd-country-trigger" type="button" data-act="country"><span>${t("Флаг")}</span>
        <span class="qd-name">${flag(draft.flag)}${esc(draft.countries.find(country=>country.code===draft.flag)?.name || t("Без флага"))} <span aria-hidden="true">&gt;</span></span></button>` +
      secretInput("password","Пароль туннеля",draft.password) +
      link("hashes","VK-хеши","[ "+hashCount()+" / 4 ] >") +
      `<div class="qd-power"><header><label for="workers">${t("Мощность")}</label><output id="power-output">${value}</output></header>
      <input id="workers" name="workers" type="range" min="${max ? 9 : 0}" max="${max}" step="9" value="${value}" ${max?"":"disabled"}>
      <small>${max ? t("Макс. {n} потоков · шаг 9").replace("{n}",max) : t("Добавьте VK-хеш")}</small></div>` +
      group("Режим VK",choice("draft.vk:vkcalls","Звонок",draft.vk==="vkcalls")+choice("draft.vk:legacy","Капча",draft.vk==="legacy")) +
      sw("draft.account","Вход через аккаунт VK",draft.account) +
      (draft.account ? link("vk-login","Войти в VK",">") : "") +
      (draft.id && state.groups.length ? `<label class="qd-field"><span>${t("Папка")}</span><select name="groupId"><option value="">${t("Без папки")}</option>${state.groups.map(g=>`<option value="${esc(g.id)}" ${draft.groupId===g.id?"selected":""}>${esc(g.name)}</option>`).join("")}</select></label>` :"") +
      submit() + (draft.id ? link("export","Экспорт профиля") + `<div class="qd-side-actions">${btn("profile.delete","Удалить профиль")}</div>` : "")));
  }
  function countryRows() {
    const query = countryQuery.toLocaleLowerCase().trim().replace(/ё/g,"е");
    const countries = [{code:"",name:t("Без флага")},...draft.countries].filter(({code,name}) =>
      (name+" "+code).toLocaleLowerCase().replace(/ё/g,"е").includes(query));
    return countries.map(({code,name}) => `<button class="qd-row qd-country-option" type="button"
        data-act="flag:${esc(code)}" aria-pressed="${code===draft.flag}"><span class="qd-name">${flag(code)}${esc(name)}</span>
        <span class="qd-mono" aria-hidden="true">${code===draft.flag ? "[+]" : "[ ]"}</span></button>`)
      .join("") || `<p class="qd-note">${t("Ничего не найдено")}</p>`;
  }
  function countryPanel() {
    return section("Выбор страны",`<div class="qd-country-search">
      ${input("country-search","Страна",countryQuery,`type="search" autocomplete="off" placeholder="${t("Название страны или код")}"`)}
      </div><div class="qd-country-options" data-countries>${countryRows()}</div>`);
  }
  let hashMessage = "";
  function hashesPanel() {
    return section("VK-хеши профиля",form("hashes",hashes.map((value,index) => {
      const id = `hash-state-${index}`;
      return `<div class="qd-hash" data-slot="${index}">
        ${input("slot"+index,t("Слот")+" "+(index+1),value,`autocomplete="off" spellcheck="false" aria-describedby="${id}" placeholder="${t("Ссылка или хеш звонка")}"`)}
        <span class="qd-hash-status" id="${id}" role="status"></span></div>`;
    }).join("") + btn("hash-check","Проверить") + '<p class="qd-error qd-hash-error" role="status"></p>' + submit()));
  }
  const hashLabels = {
    checking:"Проверяется…", solving_captcha:"Решается капча…", ok:"Работает",
    dead:"Звонок закрыт или хеш недействителен", invalid:"Неверный формат хеша",
    captcha:"Нужна капча VK", limited:"VK ограничил частоту запросов",
    network:"Ошибка сети или DNS", cancelled:"Проверка остановлена",
  };
  function paintHashes() {
    if (state.section !== "hashes") return;
    for (const node of phone.querySelectorAll("[data-slot]")) {
      const status = hashStatuses[Number(node.dataset.slot)] || "";
      const field = node.querySelector("input");
      node.dataset.status = status==="ok" ? "ok" : ["dead","invalid"].includes(status) ? "bad" : status ? "pending" : "";
      field.setAttribute("aria-invalid", node.dataset.status==="bad");
      node.querySelector(".qd-hash-status").textContent = status ? t(hashLabels[status] || "Не удалось проверить") : "";
    }
    const form = phone.querySelector('[data-form="hashes"]');
    form.querySelector(".qd-hash-error").textContent = t(hashMessage);
    form.querySelector('[data-act="hash-check"]').disabled = !!hashProbe;
  }
  function applyHashStatus(probe, result) {
    const slot = result.slot-1;
    if (probe.fields === hashes && hashes[slot] === probe.values[slot]) hashStatuses[slot] = result.status;
  }
  window.qwdttHashCheck = progress => {
    if (!hashProbe || hashProbe.id !== progress.id) return;
    applyHashStatus(hashProbe, progress);
    paintHashes();
  };
  async function checkHashes() {
    if (busy || hashProbe) return;
    hashMessage = hashes.some(value => value.trim()) ? "" : "Введите VK-хеш";
    if (hashMessage) { paintHashes(); return; }
    const probe = hashProbe = {fields: hashes, values: [...hashes]};
    hashStatuses = {};
    for (const [slot,value] of probe.values.entries()) if (value.trim()) hashStatuses[slot] = "checking";
    paintHashes();
    try {
      await run("hash.check",{hashes:probe.values,vk:draft.vk},results => {
        for (const result of results) applyHashStatus(probe, result);
      },error => { if (hashes===probe.fields) hashMessage = error.message; },id => { probe.id = id; });
    } finally {
      if (hashes===probe.fields) {
        for (const [slot,value] of Object.entries(hashStatuses)) if (value==="checking" || value==="solving_captcha") delete hashStatuses[slot];
      }
      hashProbe = null;
      paintHashes();
    }
  }
  function portsPanel() {
    return section("Порты",form("ports",
      port("dtls","Порт сервера DTLS",ports.dtls) + port("wg","Порт сервера WireGuard",ports.wg) +
      port("local","Локальный порт VPN",ports.local) + submit()));
  }
  function dnsPanel() {
    return section("DNS для VK",form("dns",
      link("dns-list","Провайдер DNS",dnsList?"^":"v",dnsLabels[dns.key] || dns.key) +
      (dnsList ? dnsGroups.map(([name,items])=>`<section class="qd-dns-group"><h4>${esc(t(name))}</h4>${items.map(([id,label,meta])=>link("dns-provider:"+id,label,dns.key===id?"[+]":">",meta)).join("")}</section>`).join("") : "") +
      (dns.key==="custom"?input("ip","IP-адреса DNS",dns.ip,'required placeholder="1.1.1.1, 8.8.8.8"'):dns.key==="doh-custom"?input("url","URL DoH",dns.url,'required type="url" placeholder="https://…/dns-query"'):"") +
      submit() + btn("dns-test","Проверить DNS")));
  }
  function serverPanel() {
    return section(server.id ? "Управление сервером" : "Новый сервер",form("server",
      input("name","Название",server.name,'required') + input("host","IP или домен",server.host,'required') +
      input("login","Логин SSH",server.login,'required') + port("port","Порт SSH",server.port || 22) +
      secretInput("password","Пароль SSH",server.password) + secretInput("adminPassword","Пароль админ-панели",server.adminPassword) +
      `<label class="qd-row"><span>${t("Вход по SSH-ключу")}</span><input type="checkbox" name="useKey" ${server.useKey?"checked":""}></label>
      <label class="qd-field"><span>${t("Приватный SSH-ключ")}</span><textarea name="privateKey" spellcheck="false" placeholder="${esc(server.keyName || t("Вставьте ключ"))}"></textarea></label>` +
      secretInput("keyPassphrase","Пароль SSH-ключа") + port("dtls","Порт DTLS",server.dtls || 56000) + port("wg","Порт WireGuard",server.wg || 56001) +
      submit() + (server.id ? `<div class="qd-side-actions">${btn("server.access","Доступы")}${btn("server.deploy","Установка / обновление")}${btn("server.remove","Убрать из списка")}</div>` : "")));
  }
  function panel() {
    switch(state.section) {
      case "mode": return networkPanel();
      case "settings": return settingsPage();
      case "dns": return dnsPanel();
      case "edit": return profilePanel();
      case "ports": return portsPanel();
      case "country": return countryPanel();
      case "hashes": return hashesPanel();
      case "export": return section("Экспорт профиля",`<img class="qd-qr" alt="${t("QR профиля")}" src="${esc(exported.qr)}"><div class="qd-side-actions">${btn("copy-profile","Копировать ссылку")}${btn("save-profile-file","Сохранить файл")}</div>`);
      case "add": return section("Добавить профиль",link("edit","Вручную")+link("import.file","Из файла")+link("import.clip","Из буфера")+link("import.qr","Сканировать QR-код")+link("import.paste","Вставить настройки")+link("subscribe","Подписка"));
      case "paste": return section("Импорт профиля",form("paste",`<label class="qd-field"><span>${t("Ссылка или конфигурация")}</span><textarea name="raw" required>${esc(importText)}</textarea></label>`+submit("Импортировать")));
      case "subscribe": return section("Подписка",form("subscribe",input("url","URL подписки","",'required type="url"')+submit("Добавить"))+
        state.subscriptions.map(s=>link("subscription.refresh:"+s.id,s.name,"Обновить")).join(""));
      case "apps": return section("Приложения",form("apps",`<div class="qd-app-list">${routeApps.map(a=>`<label class="qd-app">
        <span class="qd-app-icon" aria-hidden="true"><span>[+]</span><img src="icons/${encodeURIComponent(a.id)}.png" alt="" loading="lazy" draggable="false"></span>
        <span>${esc(a.label)}<small>${esc(a.id)}</small></span><input type="checkbox" name="app" value="${esc(a.id)}" ${a.selected?"checked":""}></label>`).join("")}</div>`+submit()));
      case "server": return serverPanel();
      case "access": return section("Доступы",btn("access.new","Создать пароль","[+]")+access.map(a=>link("access.edit:"+a.index,a.name,a.active?"[+]":"[ ]",t("{n} устройств").replace("{n}",a.devices))).join(""));
      case "access-edit": return section("Доступ",form("access",input("name","Имя",passwordEdit.name,'required')+input("max","Лимит устройств",passwordEdit.max,'type="number" min="1" required')+input("days","Продлить, дней","",'type="number" min="1"')+(passwordEdit.index==null?input("hash","VK-хеш","",'spellcheck="false"'):"")+submit())+(passwordEdit.index!=null?btn("access.toggle",passwordEdit.active?"Отключить":"Включить")+btn("access.share","Поделиться"):""));
      case "socks": return section("SOCKS5",form("socks",port("port","Локальный порт",state.socksPort || 1080)+input("user","Логин",socksSettings.user)+secretInput("password","Пароль",socksSettings.password)+`<label class="qd-row"><span>${t("Авторизация")}</span><input type="checkbox" name="auth" ${state.socksAuth?"checked":""}></label>`+submit()));
      default: return "";
    }
  }
  function pickerView() {
    const p = current();
    return `<div class="qd-picker"><button class="qd-profile-pick cursor-interaction" data-act="picker" aria-expanded="${picker}">
      ${p ? profileTitle(p) : `<span>${t("Выбрать профиль")}</span>`}<span aria-hidden="true">${picker?"^":"v"}</span></button>
      <div class="qd-picker-panel" ${picker?"":"hidden"}>${state.profiles.map(item=>`<button class="qd-row" data-act="profile:${esc(item.id)}" ${item.id===state.active?'aria-current="true"':""}>${profileTitle(item)}<span class="qd-mono" aria-hidden="true">${item.id===state.active?"✓":">"}</span></button>`).join("")}</div>
    </div>`;
  }
  function render(focus="",reset=false) {
    const previous = reset ? 0 : (phone.querySelector(".qd-main")?.scrollTop || 0);
    const isHome = state.page==="home" && !state.section;
    const content = state.section ? panel() : ({
      home, profiles:profilePage, settings:settingsPage, bypass:bypassPage, servers:serversPage,
      logs:()=>head("Логи",`<div>${btn("logs.copy","Копировать","[=]")}${btn("logs.share","Поделиться","[>]")}${btn("logs.clear","Очистить","[x]")}</div>`)+
        `<div data-stages>${stages()}</div><div class="qd-block qd-logs qd-mono" data-log>${logEntries()}</div>`,
    })[state.page]();
    document.documentElement.lang = state.lang;
    phone.innerHTML = `<header class="qd-chrome"><button class="qd-icon cursor-interaction" data-act="back" aria-label="${t("Назад")}">&lt;-</button>
      <img class="qd-brand" src="${logo}" alt="qWDTT" width="387" height="203">
      <button class="qd-icon qd-settings-icon cursor-interaction" data-act="settings" aria-label="${t("Настройки")}"><span class="qd-mono" aria-hidden="true">*</span></button></header>
      ${isHome ? pickerView() : ""}
      <div class="qd-main${isHome ? " qd-main-home" : ""}">${content}</div>
      <nav class="qd-nav" aria-label="${t("Разделы qWDTT")}">${tabs.filter(([id])=>id!=="servers" || state.role==="admin").map(([id,glyph,label])=>
        `<button data-act="${id}" ${state.page===id && !state.section?'aria-current="page"':""}><span class="qd-glyph" aria-hidden="true">${esc(glyph)}</span>${t(label)}</button>`).join("")}</nav>`;
    phone.querySelector(".qd-main").scrollTop = previous;
    const power = phone.querySelector("#workers");
    if (power) updatePower(power);
    paintPing();
    paintHashes();
    paintArtwork();
    showGalaxy();
    if (focus) [...phone.querySelectorAll("[data-act]")].find(el=>el.dataset.act===focus)?.focus({preventScroll:true});
  }
  window.qwdttTunnel = update => {
    const changed = state.link !== update.link;
    const newSession = update.session != null && update.session !== state.session;
    Object.assign(state,update);
    if (changed || newSession) {
      tunnelPing = {};
      if (state.link === "up" && state.session) measureTunnelPing();
      const button = phone.querySelector('[data-act="ping"]');
      if (button) button.disabled = !!profilePing || lock();
    }
    if (state.section && !(state.section==="mode" && changed)) return;
    if (state.page === "home" && !state.section) {
      const header = phone.querySelector(".qd-hero");
      if (changed && header) {
        header.dataset.link = state.link;
        header.querySelector("h2").textContent = t(labels[state.link]);
        const controls = phone.querySelector("[data-toggle]");
        controls.innerHTML = tunnelAction();
        showGalaxy();
      }
      const stage = phone.querySelector("[data-stages]");
      if (stage) stage.innerHTML = stages();
      paintPing();
      const error = phone.querySelector("[data-error]");
      if (error) error.textContent = tunnelError();
    }
    if (state.section === "mode" && changed) render();
    const log = phone.querySelector("[data-log]");
    if (log) {
      log.innerHTML = logEntries();
      phone.querySelector("[data-stages]").innerHTML = stages();
    }
    for (const node of phone.querySelectorAll("[data-workers]")) node.textContent = state.workers;
  };
  function go(next) {
    if (next !== state.page) pages.push(state.page);
    state.page=next; state.section=""; panels.length=0; picker=false;
    draft=ports=socksSettings=hashes=dns=exported=server=access=passwordEdit=null;
    render("",true);
  }
  function open(next) {
    if (state.section) panels.push(state.section);
    state.section=next; picker=false;
    render("back",true);
  }
  function back() {
    const dialog = phone.querySelector(".qd-delete-dialog[open]");
    if (dialog) { if (!busy) closeProfileDelete(dialog); return; }
    if (picker) { picker=false; render("picker"); return; }
    if (state.section) {
      const closed=state.section;
      state.section=panels.pop() || "";
      if (closed==="ports") ports=null;
      if (closed==="socks") socksSettings=null;
      if (closed==="edit") draft=null;
      render("",true);
      return;
    }
    state.page=pages.pop() || (state.page==="home" ? "profiles" : "home");
    render("",true);
  }
  window.qwdttBack = back;

  function closeProfileDelete(dialog) {
    dialog.close();
    dialog.remove();
  }

  function confirmProfileDelete() {
    if (busy || !draft?.id || phone.querySelector(".qd-delete-dialog")) return;
    const dialog = document.createElement("dialog");
    const profile = state.profiles.find(item => item.id === draft.id);
    dialog.className = "qd-delete-dialog";
    dialog.setAttribute("aria-labelledby","qd-delete-title");
    dialog.setAttribute("aria-describedby","qd-delete-name");
    dialog.innerHTML = `<h2 id="qd-delete-title">${t("Удалить этот профиль?")}</h2>
      <p id="qd-delete-name">${esc(profile?.name || draft.name)}</p>
      <p class="qd-error" data-delete-error role="status"></p>
      <div class="qd-delete-actions">
        <button autofocus type="button" data-act="profile.delete.cancel">${t("Отмена")}</button>
        <button type="button" data-act="profile.delete.confirm">${t("Удалить")}</button>
      </div>`;
    dialog.addEventListener("cancel",event=>{
      event.preventDefault();
      if (!busy) closeProfileDelete(dialog);
    });
    phone.append(dialog);
    dialog.showModal();
  }

  async function run(action,args,callback,onError=showError,onRequest) {
    if (busy) return;
    busy=true;
    try { await callback(await native(action,args,onRequest)); }
    catch(error) { onError(error); }
    finally { busy=false; }
  }
  const mutate = (action,args={}) => run(action,args,refresh);
  function updateDraft() {
    const node=phone.querySelector('[data-form="profile"]');
    if (!node || !draft) return;
    const data=new FormData(node);
    draft.name=data.get("name") ?? draft.name;
    draft.peer=data.get("peer") ?? draft.peer;
    draft.password=data.get("password") ?? draft.password;
    draft.workers=Number(data.get("workers") || draft.workers || 9);
    draft.groupId=data.get("groupId") ?? draft.groupId;
  }
  function updatePower(node) {
    const total = Number(node.max)-Number(node.min);
    const progress = total ? (Number(node.value)-Number(node.min))*100/total : 0;
    node.style.setProperty("--qd-progress", progress+"%");
    phone.querySelector("#power-output").value = node.value;
  }
  async function measureProfilePing() {
    if (busy || profilePing || lock()) return;
    const shown = state.profiles.filter(p => !activeGroup || p.groupId===activeGroup);
    const profile = shown.find(p=>p.id===state.active) || shown[0];
    if (!profile) return;
    profilePing = profile.id;
    render();
    await run("profile.ping",{id:profile.id},refresh);
    profilePing = "";
    if (state.page==="profiles" && !state.section) render("ping");
  }
  phone.addEventListener("click",async event => {
    const target=event.target.closest("button[data-act]");
    if (!target || target.disabled) return;
    const [action,a,b]=target.dataset.act.split(":");
    if (tabs.some(([id])=>id===action) || action==="settings") { go(action); return; }
    switch(action) {
      case "back": back(); return;
      case "picker": if (!state.profiles.length) go("profiles"); else { picker=!picker; render("picker"); } return;
      case "toggle": await run("toggle",{},()=>{}); return;
      case "tunnel-ping": await measureTunnelPing(); return;
      case "profile": picker=false; await mutate("profile.select",{id:a}); return;
      case "group": activeGroup=a; render(); return;
      case "mode": case "add": case "subscribe": open(action); return;
      case "edit": await run("profile.edit",{id:a || ""},value=>{draft={...value,hashes:slots(value.hashes)};open("edit");}); return;
      case "country": updateDraft(); countryQuery=""; open("country"); return;
      case "flag": draft.flag=a; back(); return;
      case "hashes": updateDraft(); hashes=slots(draft.hashes); hashStatuses={}; hashMessage=""; open("hashes"); return;
      case "draft.account": updateDraft(); draft.account=!draft.account; render("draft.account"); return;
      case "draft.vk": updateDraft(); draft.vk=a; render(target.dataset.act); return;
      case "vk-login": updateDraft(); await run("vk.login",{},showError); return;
      case "ports": await run("ports.read",{},value=>{ports=value;open("ports");}); return;
      case "dns": dns={...state.dns}; dnsList=false; open("dns"); return;
      case "dns-list": dnsList=!dnsList; render("dns-list"); return;
      case "dns-provider": dns.key=a; dnsList=false; render("dns-list"); return;
      case "dns-test": await run("dns.test",dns,value=>showError(value)); return;
      case "value": await mutate("setting",{key:a,value:b}); return;
      case "bool": await mutate("setting",{key:a,value:!state[a]}); return;
      case "role": await mutate("setting",{key:"role",value:a}); return;
      case "lang": await mutate("setting",{key:"lang",value:a}); return;
      case "route-mode": await mutate("setting",{key:"whitelist",value:a==="1"}); return;
      case "hash-check": await checkHashes(); return;
      case "ping": await measureProfilePing(); return;
      case "export": updateDraft(); await run("profile.export",{id:draft.id},value=>{exported=value;open("export");}); return;
      case "copy-profile": await run("copy",{text:exported.link},()=>showError("Скопировано")); return;
      case "save-profile-file": await run("export.file",{kind:"profile",id:exported.id},()=>{}); return;
      case "import.paste": importText=""; open("paste"); return;
      case "import.file": case "import.qr": case "import.clip":
        if (state.section==="add") back();
        await mutate(action); return;
      case "subscription.refresh": await mutate(action,{id:a}); return;
      case "profile.delete": confirmProfileDelete(); return;
      case "profile.delete.cancel": back(); return;
      case "profile.delete.confirm": {
        const dialog=target.closest("dialog");
        if (busy || !dialog?.open) return;
        for (const button of dialog.querySelectorAll("button")) button.disabled=true;
        await run("profile.delete",{id:draft.id},async()=>{
          closeProfileDelete(dialog);
          back();
          await refresh();
        },error=>{
          if (dialog.open) dialog.querySelector("[data-delete-error]").textContent=t(error.message);
          else showError(error);
        });
        for (const button of dialog.querySelectorAll("button")) button.disabled=false;
        return;
      }
      case "apps": await run("bypass.apps",{},value=>{routeApps=value;open("apps");}); return;
      case "bypass-import": await mutate(action); return;
      case "bypass-export": await run("export.file",{kind:"bypass"},()=>{}); return;
      case "logs.copy": case "logs.share": case "logs.clear": case "notifications": case "update": case "github": await run(action,{},value=>{if(value)showError(value);}); return;
      case "socks": await run("socks.read",{},value=>{socksSettings=value;open("socks");}); return;
      case "server.new": server={login:"root",port:22}; open("server"); return;
      case "server.edit": await run("server.read",{id:a},value=>{server=value;open("server");}); return;
      case "server.remove": if (confirm(t("Убрать сервер из списка?"))) await run(action,{id:server.id},async()=>{back();await refresh();}); return;
      case "server.deploy": if (confirm(t("Установить или обновить серверную часть на этом VPS?"))) await run(action,{id:server.id},value=>showError(value)); return;
      case "server.access": await run(action,{id:server.id},value=>{access=value;open("access");}); return;
      case "access.new": passwordEdit={max:1}; open("access-edit"); return;
      case "access.edit": passwordEdit={...access[Number(a)]}; open("access-edit"); return;
      case "access.toggle": await run(action,{id:server.id,index:passwordEdit.index,active:!passwordEdit.active},value=>{access=value;back();}); return;
      case "access.share": await run(action,{id:server.id,index:passwordEdit.index},()=>{}); return;
    }
  });
  phone.addEventListener("input",event=>{
    const node=event.target, kind=node.closest("form")?.dataset.form;
    if (kind==="profile" && node.name==="workers") updatePower(node);
    if (kind==="hashes" && node.name.startsWith("slot")) {
      const slot = Number(node.name.slice(4));
      hashes[slot] = node.value;
      delete hashStatuses[slot];
      hashMessage = "";
      paintHashes();
    }
    if (node.name==="country-search") {
      countryQuery=node.value;
      phone.querySelector("[data-countries]").innerHTML=countryRows();
    }
    if (kind==="dns") dns[node.name]=node.value;
  });
  phone.addEventListener("submit",async event=>{
    event.preventDefault();
    if(busy)return;
    const inputData=new FormData(event.target), data=Object.fromEntries(inputData);
    const name=event.target.dataset.form;
    if (name==="hashes") {
      draft.hashes=slots(hashes).map(v=>v.trim());
      back();
      return;
    }
    try {
      busy=true;
      switch(name) {
        case "profile": {
          updateDraft();
          const {countries,...profile} = draft;
          await native("profile.save",profile);
          break;
        }
        case "ports": await native("ports.save",data); break;
        case "dns": await native("dns.save",dns); break;
        case "routes": await native("bypass.routes",data); break;
        case "apps": await native("bypass.select",{apps:inputData.getAll("app")}); break;
        case "paste": await native("profile.import",data); break;
        case "subscribe": await native("subscription.add",data); break;
        case "server": await native("server.save",{...data,id:server.id || "",useKey:inputData.has("useKey")}); break;
        case "socks": await native("socks.save",{...data,auth:inputData.has("auth")}); break;
        case "access": access=await native("access.save",{...data,id:server.id,index:passwordEdit.index}); break;
      }
      if (name==="paste" || name==="subscribe" || name==="profile" && !draft.id) go("profiles");
      else if (state.section) back();
      await refresh();
    } catch(error) { showError(error); }
    finally { busy=false; }
  });
  phone.addEventListener("error",event=>{ if (event.target instanceof HTMLImageElement) event.target.hidden=true; },true);
  phone.addEventListener("contextmenu",event=>{ if (!event.target.closest(".qd-field input, textarea")) event.preventDefault(); });
  document.addEventListener("keydown",e=>{if(e.key==="Escape"){e.preventDefault();back();}});
  reducedMotion.addEventListener("change",showGalaxy);
  render();
  refresh().catch(showError);
})();
