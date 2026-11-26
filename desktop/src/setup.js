const body = document.body;
const form = document.querySelector("#connect");
const field = document.querySelector("#hub");
const clearButton = document.querySelector("#hub-clear");
const connectSubmit = document.querySelector("#connect-submit");
const connectSpinner = connectSubmit.querySelector(".btn-spinner");
const connectLabel = connectSubmit.querySelector(".btn-label");
const inputDetectPill = document.querySelector("#input-detect-pill");
const inputDetectText = document.querySelector("#input-detect-text");
const inputPinBadge = document.querySelector("#input-pin-badge");
const linkInputCallout = document.querySelector("#link-input-callout");
const useAsLinkButton = document.querySelector("#use-as-link");
const extractHubOnlyButton = document.querySelector("#extract-hub-only");

const errorCard = document.querySelector("#error");
const errorTitle = document.querySelector("#error-title");
const errorBody = document.querySelector("#error-body");
const errorTips = document.querySelector("#error-tips");
const errorTechWrap = document.querySelector("#error-tech-wrap");
const errorTechCode = document.querySelector("#error-tech-code");

const title = document.querySelector("#setup-title");
const subtitle = document.querySelector("#setup-subtitle");
const heroBadge = document.querySelector("#hero-badge");
const recovery = document.querySelector("#connection-recovery");
const recoveryHostRow = document.querySelector("#recovery-host-row");
const recoveryHost = document.querySelector("#recovery-host");
const retryButton = document.querySelector("#retry");
const retrySpinner = retryButton.querySelector(".btn-spinner");
const retryLabel = retryButton.querySelector(".btn-label");
const localSettingsButton = document.querySelector("#local-settings");

const onboardingBackBar = document.querySelector("#onboarding-back-bar");
const backToHubButton = document.querySelector("#back-to-hub");
const toggleHelpButton = document.querySelector("#toggle-help");
const onboardingHelp = document.querySelector("#onboarding-help");
const openLinkViewButton = document.querySelector("#open-link-view");

const state = document.querySelector("#link-state");
const linkStatusDot = document.querySelector("#link-status-dot");
const linkStatusPill = document.querySelector("#link-status-pill");
const railLinkDot = document.querySelector("#rail-link-dot");
const linkGuideCard = document.querySelector("#link-guide-card");
const result = document.querySelector("#link-result");
const pairInput = document.querySelector("#pair-link");
const pairSubmit = document.querySelector("#pair-submit");
const pairSpinner = pairSubmit.querySelector(".btn-spinner");
const pairLabel = pairSubmit.querySelector(".btn-label");
const linkLoginPrompt = document.querySelector("#link-login-prompt");
const usePairedHubButton = document.querySelector("#use-paired-hub");
const usePairedHubSpinner = usePairedHubButton.querySelector(".btn-spinner");
const usePairedHubLabel = usePairedHubButton.querySelector(".btn-label");

const pluginToggle = document.querySelector("#claude-plugin");
const pluginStatus = document.querySelector("#claude-plugin-status");
const loginToggle = document.querySelector("#launch-at-login");
const loginStatus = document.querySelector("#launch-at-login-status");

const hubSection = document.querySelector("#hub-settings");
const linkSection = document.querySelector("#link-settings");
const desktopSection = document.querySelector("#desktop-settings");
const railTabs = Array.from(document.querySelectorAll("[data-tab-target]"));

let words = {};
let currentMode = "onboarding";
let currentView = "hub";
let currentTab = "hub";
let lastDetectedLink = "";
let lastSuggestedHub = "";
let pairedSuggestedHub = "";
let isConnecting = false;
let isPairing = false;

function t(key, variables = {}) {
  return (words[key] || key).replace(/\{(\w+)\}/g, (_, name) => variables[name] ?? "");
}

function stripQuotes(raw) {
  const s = raw.trim();
  if (s.length >= 2 && ((s[0] === "'" && s[s.length - 1] === "'") || (s[0] === '"' && s[s.length - 1] === '"'))) {
    return s.slice(1, -1).trim();
  }
  return s;
}

function extractLinkInfo(raw) {
  const cleaned = stripQuotes(raw);
  const match = cleaned.match(/(?:persona|jarvis):\/\/link\?[^\s'"<>]+/i);
  if (!match) return null;
  try {
    const parsed = new URL(match[0]);
    if (parsed.host !== "link") return null;
    const urlParam = parsed.searchParams.get("url")?.trim();
    const tls = parsed.searchParams.get("tls")?.trim();
    let suggestedHub = "";
    if (urlParam) {
      const base = new URL(urlParam);
      if (base.protocol === "https:") {
        suggestedHub = `${base.origin}${base.pathname.replace(/\/+$/, "")}/`;
      } else if (tls && /^\d{1,5}$/.test(tls)) {
        const host = base.hostname.includes(":") && !base.hostname.startsWith("[") ? `[${base.hostname}]` : base.hostname;
        suggestedHub = `https://${host}:${tls}/`;
      } else {
        suggestedHub = `${base.origin}${base.pathname.replace(/\/+$/, "")}/`;
      }
    }
    return { linkURL: parsed.href, suggestedHub };
  } catch {
    return null;
  }
}

function detectInputKind(raw) {
  const cleaned = stripQuotes(raw);
  if (!cleaned) return { kind: "empty" };
  const linkInfo = extractLinkInfo(cleaned);
  if (linkInfo) return { kind: "link", ...linkInfo };
  if (/^(?:persona|jarvis):\/\/pair\?/i.test(cleaned)) {
    try {
      const u = new URL(cleaned);
      const hasToken = Boolean(u.searchParams.get("token")?.trim());
      const hasPin = /^[A-Za-z0-9_-]{43}$/.test(u.searchParams.get("fp")?.trim() ?? "");
      if (hasToken) return { kind: "pair", hasPin };
    } catch { /* fall through */ }
  }
  if (/^https?:\/\//i.test(cleaned)) {
    try {
      const u = new URL(cleaned);
      const hashToken = new URLSearchParams(u.hash.replace(/^#/, "")).get("token")?.trim();
      const queryToken = u.searchParams.get("token")?.trim();
      if (hashToken || queryToken) return { kind: "token" };
    } catch { /* fall through */ }
  }
  return { kind: "hub" };
}

function updateInputDetection() {
  const value = field.value;
  clearButton.hidden = value.length === 0 || isConnecting;
  const detected = detectInputKind(value);
  lastDetectedLink = "";
  lastSuggestedHub = "";
  inputDetectPill.hidden = true;
  inputPinBadge.hidden = true;
  linkInputCallout.hidden = true;

  if (detected.kind === "pair") {
    inputDetectText.textContent = t("hint_pair_detected");
    inputPinBadge.hidden = !detected.hasPin;
    inputDetectPill.hidden = false;
    if (!isConnecting) connectLabel.textContent = t("connect_and_signin");
    return;
  }
  if (detected.kind === "token") {
    inputDetectText.textContent = t("hint_token_detected");
    inputDetectPill.hidden = false;
    if (!isConnecting) connectLabel.textContent = t("connect_and_signin");
    return;
  }
  if (detected.kind === "link") {
    lastDetectedLink = detected.linkURL;
    lastSuggestedHub = detected.suggestedHub || "";
    extractHubOnlyButton.hidden = !lastSuggestedHub;
    linkInputCallout.hidden = false;
  }
  if (!isConnecting) connectLabel.textContent = t("continue");
}

function clearError() {
  errorCard.hidden = true;
  errorTitle.textContent = "";
  errorBody.textContent = "";
  errorTips.replaceChildren();
  errorTips.hidden = true;
  errorTechWrap.hidden = true;
  errorTechWrap.open = false;
  errorTechCode.textContent = "";
}

function showError(response) {
  const code = typeof response === "object" && response ? response.errorCode : "";
  const technical = typeof response === "object" && response ? response.technical : "";
  const rawMessage = typeof response === "string" ? response : response?.error;

  const known = {
    invalid_url: ["err_invalid_url_title", "err_invalid_url_body", []],
    cert_untrusted: ["err_cert_untrusted_title", "err_cert_untrusted_body", ["err_cert_untrusted_tip_1", "err_cert_untrusted_tip_2"]],
    cert_pin_mismatch: ["err_cert_pin_mismatch_title", "err_cert_pin_mismatch_body", []],
    cert_expired: ["err_cert_expired_title", "err_cert_expired_body", []],
    tls_protocol: ["err_tls_protocol_title", "err_tls_protocol_body", []],
    connection_refused: ["err_connection_refused_title", "err_connection_refused_body", []],
    dns_failed: ["err_dns_failed_title", "err_dns_failed_body", []],
    timeout: ["err_timeout_title", "err_timeout_body", []],
    offline: ["err_offline_title", "err_offline_body", []],
  };

  const entry = known[code];
  if (entry) {
    errorTitle.textContent = t(entry[0]);
    errorBody.textContent = t(entry[1]);
    errorTips.replaceChildren();
    for (const tipKey of entry[2]) {
      const li = document.createElement("li");
      li.textContent = t(tipKey);
      errorTips.appendChild(li);
    }
    errorTips.hidden = entry[2].length === 0;
  } else {
    errorTitle.textContent = t("err_unknown_title");
    errorBody.textContent = rawMessage || t("err_unknown_body");
    errorTips.replaceChildren();
    errorTips.hidden = true;
  }

  const techLine = technical || (entry && rawMessage ? rawMessage : "");
  if (techLine) {
    errorTechCode.textContent = techLine;
    errorTechWrap.hidden = false;
  } else {
    errorTechWrap.hidden = true;
  }
  errorCard.hidden = false;
}

function syncLayout() {
  body.dataset.mode = currentMode;
  body.dataset.view = currentView;
  body.dataset.tab = currentTab;

  const isRecovery = currentMode === "recovery";
  const isOnboarding = currentMode === "onboarding";
  const isSettings = currentMode === "settings";

  recovery.hidden = !isRecovery;
  heroBadge.hidden = !isRecovery;
  onboardingBackBar.hidden = !(isOnboarding && currentView === "link");

  if (isRecovery) {
    title.textContent = t("hub_connection_title");
    subtitle.hidden = true;
    hubSection.classList.add("is-tab-hidden");
    linkSection.classList.add("is-tab-hidden");
    desktopSection.classList.add("is-tab-hidden");
  } else if (isOnboarding) {
    title.textContent = t("welcome");
    subtitle.textContent = t("welcome_subtitle");
    subtitle.hidden = false;
    hubSection.classList.toggle("is-tab-hidden", currentView !== "hub");
    linkSection.classList.toggle("is-tab-hidden", currentView !== "link");
    desktopSection.classList.add("is-tab-hidden");
  } else if (isSettings) {
    subtitle.hidden = false;
    hubSection.classList.toggle("is-tab-hidden", currentTab !== "hub");
    linkSection.classList.toggle("is-tab-hidden", currentTab !== "link");
    desktopSection.classList.toggle("is-tab-hidden", currentTab !== "desktop");
    for (const tab of railTabs) {
      const active = tab.dataset.tabTarget === currentTab;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-selected", String(active));
    }
  }
}

function showConnectionFailure(address) {
  currentMode = "recovery";
  try {
    const parsed = new URL(address);
    recoveryHost.textContent = parsed.host + (parsed.pathname !== "/" ? parsed.pathname.replace(/\/$/, "") : "");
    recoveryHostRow.hidden = false;
  } catch {
    recoveryHostRow.hidden = true;
  }
  syncLayout();
}

function showLocalSettings() {
  currentMode = "settings";
  currentTab = "hub";
  clearError();
  syncLayout();
  field.focus();
}

function setConnecting(busy) {
  isConnecting = busy;
  field.disabled = busy;
  clearButton.hidden = busy || field.value.length === 0;
  connectSubmit.disabled = busy;
  connectSpinner.hidden = !busy;
  connectLabel.textContent = busy
    ? t("connecting")
    : (["pair", "token"].includes(detectInputKind(field.value).kind) ? t("connect_and_signin") : t("continue"));
}

function setPairing(busy) {
  isPairing = busy;
  pairInput.disabled = busy;
  pairSubmit.disabled = busy;
  pairSpinner.hidden = !busy;
  pairLabel.textContent = busy ? t("pairing_working") : t("pair");
}

function setLinkDot(tone) {
  const cls = `status-dot status-dot--${tone}`;
  linkStatusDot.className = cls;
  railLinkDot.className = cls;
}

function setLinkPill(text, tone) {
  if (!text) {
    linkStatusPill.hidden = true;
    linkStatusPill.textContent = "";
    return;
  }
  linkStatusPill.className = `status-pill status-pill--${tone}`;
  linkStatusPill.textContent = text;
  linkStatusPill.hidden = false;
}

function showLinkResult(text, tone) {
  result.textContent = text;
  result.className = `feedback-banner${tone ? ` feedback-banner--${tone}` : ""}`;
  result.hidden = false;
  result.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

async function refreshLink() {
  const status = await window.desktopSetup.linkStatus();
  if (!status.available) {
    state.textContent = t("link_absent");
    setLinkDot("muted");
    setLinkPill(t("not_running"), "muted");
    document.querySelector("#link-reconnect").disabled = true;
    pairInput.disabled = true;
    pairSubmit.disabled = true;
    document.querySelector("#node-install").hidden = true;
    linkGuideCard.hidden = false;
    document.querySelector("#link-install-guide").hidden = false;
    document.querySelector("#link-install").hidden = false;
    pluginToggle.disabled = true;
    pluginStatus.textContent = "";
    return;
  }
  showPlugin(status.claude_plugin);
  const link = status.link || {};
  const rawState = link.state || "";
  const stateLabels = {
    connected: t("link_status_connected"),
    connecting: t("link_status_connecting"),
    reconnecting: t("link_status_connecting"),
    disconnected: t("not_running"),
  };
  const displayState = link.pending
    ? t("link_status_pending")
    : (stateLabels[rawState] || rawState || (status.running === true ? t("link_status_connected") : t("not_running")));
  const machineLabel = status.machine_name || status.machine;
  state.textContent = t("link_state", { state: displayState }) + (machineLabel ? ` · ${machineLabel}` : "");

  if (link.pending) {
    setLinkDot("warn");
    setLinkPill(t("link_status_pending"), "warn");
  } else if (rawState === "connected" || (status.running === true && !rawState)) {
    setLinkDot("ok");
    setLinkPill(t("link_status_connected"), "ok");
  } else if (rawState === "connecting" || rawState === "reconnecting") {
    setLinkDot("warn");
    setLinkPill(t("link_status_connecting"), "warn");
  } else {
    setLinkDot("muted");
    setLinkPill(displayState, "muted");
  }

  document.querySelector("#link-reconnect").disabled = false;
  if (!isPairing) {
    pairInput.disabled = false;
    pairSubmit.disabled = false;
  }
  document.querySelector("#node-install").disabled = false;
  document.querySelector("#node-install").hidden = false;
  linkGuideCard.hidden = true;
  document.querySelector("#link-install-guide").hidden = true;
  document.querySelector("#link-install").hidden = true;
}

// The Persona plugin for Claude Code, which the resident node keeps installed.
function showPlugin(plugin) {
  const pluginState = plugin && plugin.state;
  pluginToggle.disabled = pluginState === "claude_missing" || pluginState === "unavailable";
  pluginToggle.checked = Boolean(plugin && plugin.auto && ["installed", "outdated", "error"].includes(pluginState));
  const known = ["installed", "outdated", "disabled", "not_installed", "claude_missing", "unavailable", "error"];
  const key = known.includes(pluginState) ? pluginState : "unknown";
  pluginStatus.textContent = t(`claude_plugin_${key}`, { version: (plugin && plugin.version) || "" });
}

pluginToggle.addEventListener("change", async () => {
  pluginToggle.disabled = true;
  await command({ action: pluginToggle.checked ? "plugin-install" : "plugin-uninstall" });
});

async function command(value) {
  const pairing = value.action === "pair-active";
  if (pairing) setPairing(true);
  linkLoginPrompt.hidden = true;
  showLinkResult(pairing ? t("pairing_working") : t("working"), "");
  try {
    const response = await window.desktopSetup.linkCommand(value);
    if (response.ok) {
      showLinkResult(response.stdout?.trim() || t("done"), "ok");
      if (pairing) {
        pairInput.value = "";
        if (response.suggestedHub && currentMode === "onboarding") {
          pairedSuggestedHub = response.suggestedHub;
          linkLoginPrompt.hidden = false;
        }
      }
    } else {
      showLinkResult(response.stderr?.trim() || response.stdout?.trim() || t("generic_error"), "error");
    }
    await refreshLink();
  } catch (err) {
    showLinkResult(err instanceof Error ? err.message : t("generic_error"), "error");
  } finally {
    if (pairing) setPairing(false);
  }
}

form.addEventListener("submit", async event => {
  event.preventDefault();
  if (isConnecting) return;
  clearError();
  const rawValue = field.value.trim();
  if (!rawValue) {
    showError({ errorCode: "invalid_url" });
    field.focus();
    return;
  }
  const detected = detectInputKind(rawValue);
  if (detected.kind === "link") {
    linkInputCallout.hidden = false;
    return;
  }
  setConnecting(true);
  try {
    const response = await window.desktopSetup.saveHub(rawValue);
    if (!response.ok && !response.replaced) {
      if (response.errorCode === "link_pair_input") {
        updateInputDetection();
      } else {
        showError(response);
      }
    }
  } catch {
    showError({ errorCode: "unknown" });
  } finally {
    setConnecting(false);
  }
});

field.addEventListener("input", () => {
  clearError();
  updateInputDetection();
});

clearButton.addEventListener("click", () => {
  field.value = "";
  clearError();
  updateInputDetection();
  field.focus();
});

useAsLinkButton.addEventListener("click", async () => {
  const linkValue = lastDetectedLink || field.value.trim();
  clearError();
  if (currentMode === "settings") currentTab = "link";
  else currentView = "link";
  syncLayout();
  pairInput.value = linkValue;
  if (!pairSubmit.disabled) {
    await command({ action: "pair-active", value: linkValue });
  } else {
    linkGuideCard.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }
});

extractHubOnlyButton.addEventListener("click", () => {
  if (!lastSuggestedHub) return;
  field.value = lastSuggestedHub;
  clearError();
  updateInputDetection();
  field.focus();
});

retryButton.addEventListener("click", async () => {
  retryButton.disabled = true;
  retrySpinner.hidden = false;
  retryLabel.textContent = t("working");
  clearError();
  try {
    const response = await window.desktopSetup.saveHub(field.value);
    if (!response.ok && !response.replaced) showError(t("hub_connection_retry_failed"));
  } catch {
    showError(t("hub_connection_retry_failed"));
  } finally {
    retryButton.disabled = false;
    retrySpinner.hidden = true;
    retryLabel.textContent = t("retry");
  }
});

localSettingsButton.addEventListener("click", showLocalSettings);

for (const tab of railTabs) {
  tab.addEventListener("click", () => {
    currentTab = tab.dataset.tabTarget || "hub";
    if (currentTab !== "hub") clearError();
    syncLayout();
  });
}

toggleHelpButton.addEventListener("click", () => {
  const open = onboardingHelp.hidden;
  onboardingHelp.hidden = !open;
  toggleHelpButton.setAttribute("aria-expanded", String(open));
});

openLinkViewButton.addEventListener("click", () => {
  clearError();
  currentView = "link";
  syncLayout();
});

backToHubButton.addEventListener("click", () => {
  currentView = "hub";
  syncLayout();
  field.focus();
});

usePairedHubButton.addEventListener("click", async () => {
  if (!pairedSuggestedHub || isConnecting) return;
  isConnecting = true;
  usePairedHubButton.disabled = true;
  usePairedHubSpinner.hidden = false;
  usePairedHubLabel.textContent = t("connecting");
  field.value = pairedSuggestedHub;
  clearError();
  try {
    const response = await window.desktopSetup.saveHub(pairedSuggestedHub);
    if (!response.ok && !response.replaced) {
      currentView = "hub";
      syncLayout();
      updateInputDetection();
      showError(response);
    }
  } catch {
    currentView = "hub";
    syncLayout();
    updateInputDetection();
    showError({ errorCode: "unknown" });
  } finally {
    isConnecting = false;
    usePairedHubButton.disabled = false;
    usePairedHubSpinner.hidden = true;
    usePairedHubLabel.textContent = t("use_paired_hub");
  }
});

document.querySelector("#link-refresh").addEventListener("click", () => void refreshLink());
document.querySelector("#link-install").addEventListener("click", async () => {
  const { address } = await window.desktopSetup.getHub();
  const base = address || pairedSuggestedHub || lastSuggestedHub;
  if (base) window.desktopSetup.openExternal(`${base.replace(/\/$/, "")}/machines`);
  else if (currentMode === "onboarding") {
    currentView = "hub";
    syncLayout();
    field.focus();
  }
});
document.querySelector("#link-reconnect").addEventListener("click", () => void command({ action: "reconnect" }));
document.querySelector("#node-install").addEventListener("click", () => void command({ action: "node-install" }));

document.querySelector("#pair").addEventListener("submit", event => {
  event.preventDefault();
  if (isPairing) return;
  const val = pairInput.value.trim();
  if (!val) {
    showLinkResult(t("pair_hint"), "error");
    pairInput.focus();
    return;
  }
  void command({ action: "pair-active", value: val });
});

loginToggle.addEventListener("change", async () => {
  const enabled = loginToggle.checked;
  loginToggle.disabled = true;
  try {
    const login = await window.desktopSetup.setLaunchAtLogin(enabled);
    loginToggle.checked = login.enabled;
    loginStatus.textContent = login.requiresApproval ? t("launch_requires_approval") : "";
  } finally {
    loginToggle.disabled = false;
  }
});

void window.desktopSetup.getHub().then(async ({ address, connectionFailed, mode, platform, locale, strings }) => {
  field.value = address || "";
  words = strings || {};
  document.documentElement.lang = locale || "en";
  if (platform) body.dataset.platform = platform;

  for (const element of document.querySelectorAll("[data-i18n]")) {
    element.textContent = t(element.dataset.i18n);
  }
  for (const element of document.querySelectorAll("[data-i18n-placeholder]")) {
    element.setAttribute("placeholder", t(element.dataset.i18nPlaceholder));
  }
  for (const element of document.querySelectorAll("[data-i18n-aria-label]")) {
    element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel));
  }

  if (mode === "settings") {
    currentMode = "settings";
    currentTab = "hub";
  } else if (connectionFailed && address) {
    showConnectionFailure(address);
  } else {
    currentMode = "onboarding";
    currentView = "hub";
  }

  syncLayout();
  updateInputDetection();

  const login = await window.desktopSetup.getLaunchAtLogin();
  loginToggle.checked = login.enabled;
  loginStatus.textContent = login.requiresApproval ? t("launch_requires_approval") : "";
  await refreshLink();
});
