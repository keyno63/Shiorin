"use strict";

const $ = (id) => document.getElementById(id);
const storageKey = "shiorin.accessToken";
let token = "";
try { token = sessionStorage.getItem(storageKey) || ""; } catch { /* Storage may be disabled. */ }
let offset = 0;
let total = 0;
const limit = 20;
let filters = { q: "", tag: "" };
let busy = false;

function message(text = "", error = false) {
  $("status").textContent = text;
  $("status").hidden = !text;
  $("status").classList.toggle("error", error);
}

function saveToken(value) {
  token = value;
  try {
    if (value) sessionStorage.setItem(storageKey, value);
    else sessionStorage.removeItem(storageKey);
  } catch { /* Authentication still works until the page is reloaded. */ }
}

function signedOut() {
  saveToken("");
  $("library").hidden = true;
  $("account").hidden = true;
  $("auth-panel").hidden = false;
  $("username").textContent = "";
  $("results").replaceChildren();
  $("result-summary").textContent = "";
  $("bookmark-form").reset();
  $("search-form").reset();
  offset = total = 0;
  filters = { q: "", tag: "" };
}

function signedIn(user) {
  $("username").textContent = user.username;
  $("auth-panel").hidden = true;
  $("library").hidden = false;
  $("account").hidden = false;
  $("password").value = "";
}

async function api(path, { method = "GET", body, authenticated = true } = {}) {
  const headers = { Accept: "application/json" };
  if (authenticated && token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";
  let response;
  try {
    response = await fetch(path, {
      method, headers, body: body === undefined ? undefined : JSON.stringify(body),
      cache: "no-store", signal: AbortSignal.timeout(15000),
    });
  } catch {
    throw new Error("Could not reach Shiorin. Check your connection and try again.");
  }
  if (response.status === 401 && authenticated) {
    signedOut();
    throw new Error("Your session has expired or was revoked. Please log in again.");
  }
  const data = response.status === 204 ? null : await response.json();
  if (!response.ok) throw new Error(data?.error || "The request failed. Please try again.");
  return data;
}

function pagination() {
  $("previous").disabled = busy || offset === 0;
  $("next").disabled = busy || offset + limit >= total || offset + limit > 1000000;
}

async function run(action) {
  if (busy) return;
  busy = true;
  message();
  document.querySelectorAll("input, textarea, button").forEach((el) => { el.disabled = true; });
  document.querySelector("main").setAttribute("aria-busy", "true");
  try { await action(); }
  catch (error) { message(error.message, true); }
  finally {
    busy = false;
    document.querySelectorAll("input, textarea, button").forEach((el) => { el.disabled = false; });
    document.querySelector("main").removeAttribute("aria-busy");
    pagination();
  }
}

function element(tag, text, className) {
  const el = document.createElement(tag);
  el.textContent = text;
  if (className) el.className = className;
  return el;
}

function renderBookmark(bookmark) {
  const item = document.createElement("li");
  const heading = document.createElement("h2");
  // Never interpret stored titles/notes as HTML, or open non-HTTP URLs.
  let safeURL = null;
  try {
    const parsed = new URL(bookmark.url);
    if (["http:", "https:"].includes(parsed.protocol)) safeURL = parsed.href;
  } catch { /* Show invalid legacy URLs as plain text. */ }
  const title = element(safeURL ? "a" : "span", bookmark.title);
  if (safeURL) {
    title.href = safeURL;
    title.target = "_blank";
    title.rel = "noopener noreferrer";
  }
  heading.append(title);
  item.append(heading, element("div", bookmark.url, "url"));
  if (bookmark.note) item.append(element("p", bookmark.note, "note"));
  const tags = element("div", "", "tags");
  for (const tag of bookmark.tags || []) tags.append(element("span", tag, "tag"));
  item.append(tags);
  return item;
}

async function search(nextOffset = 0, nextFilters = filters) {
  const params = new URLSearchParams({ ...nextFilters, limit, offset: nextOffset });
  const data = await api(`/bookmarks?${params}`);
  offset = data.offset;
  total = data.total;
  filters = nextFilters;
  $("results").replaceChildren(...data.items.map(renderBookmark));
  $("empty").hidden = total !== 0;
  $("empty").textContent = filters.q || filters.tag
    ? "No matching bookmarks. Try another keyword or tag."
    : "No bookmarks here yet. Save your first link using the form.";
  $("result-summary").textContent = total
    ? `${offset + 1}–${offset + data.items.length} of ${total} bookmarks`
    : "0 bookmarks";
  $("page").textContent = total ? `Page ${Math.floor(offset / limit) + 1} of ${Math.ceil(total / limit)}` : "";
  pagination();
}

$("auth-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const registering = event.submitter?.value === "register";
  const credentials = { username: $("login-username").value, password: $("password").value };
  run(async () => {
    if (registering) {
      await api("/auth/register", { method: "POST", body: credentials, authenticated: false });
      message("Account created. You can now log in.");
    } else {
      const login = await api("/auth/login", {
        method: "POST", body: { ...credentials, device_label: "Shiorin browser" }, authenticated: false,
      });
      saveToken(login.access_token);
      signedIn(login.user);
      await search();
    }
  });
});

$("logout").addEventListener("click", () => run(async () => {
  await api("/auth/logout", { method: "POST" });
  signedOut();
  message("You have been logged out.");
}));

$("search-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const nextFilters = { q: $("query").value.trim(), tag: $("tag").value.trim() };
  run(() => search(0, nextFilters));
});
$("previous").addEventListener("click", () => run(() => search(Math.max(0, offset - limit))));
$("next").addEventListener("click", () => run(() => search(offset + limit)));

$("bookmark-form").addEventListener("submit", (event) => {
  event.preventDefault();
  const bookmark = {
    title: $("title").value.trim(), url: $("url").value.trim(), note: $("note").value,
    tags: $("tags").value.split(",").map((tag) => tag.trim()).filter(Boolean),
  };
  run(async () => {
    await api("/bookmarks", { method: "POST", body: bookmark });
    $("bookmark-form").reset();
    $("search-form").reset();
    // Saving succeeded even if the subsequent refresh fails.
    try {
      await search(0, { q: "", tag: "" });
      message("Bookmark saved.");
    } catch (error) {
      message(`Bookmark saved, but the list could not be refreshed. ${error.message}`, true);
    }
  });
});

if (token) {
  $("auth-panel").hidden = true;
  run(async () => {
    try { signedIn(await api("/me")); }
    catch (error) { signedOut(); throw error; }
    await search();
  });
}
