// The one page that needs scripting: a plain form cannot hash a file, split it,
// or resume one. Hand-written rather than a library, since nothing off the shelf
// does these three things and the whole file is below.
"use strict";

const form = document.getElementById("upload");
const status = document.getElementById("status");
const csrf = document.body.dataset.csrf;

// crypto.subtle exists only in a secure context, so say so rather than failing
// on the first click.
if (!window.isSecureContext) {
  document.getElementById("insecure").hidden = false;
  form.querySelector("button").disabled = true;
}

const FORMATS = { epub: "epub", pdf: "pdf", m4b: "m4b", mp3: "mp3" };

function formatOf(name) {
  return FORMATS[name.split(".").pop().toLowerCase()];
}

async function sha256(file) {
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

function say(message) {
  status.textContent = message;
}

async function send(method, url, body, type) {
  const headers = { "X-CSRF-Token": csrf };
  if (type) headers["Content-Type"] = type;

  const res = await fetch(url, { method, headers, body });
  if (!res.ok) {
    const detail = await res.json().catch(() => ({}));
    throw new Error(detail.error || `${method} ${url} failed with ${res.status}`);
  }
  return res.json();
}

// Only the chunks the server still wants are sent, so an interrupted upload
// resumes by asking rather than starting over.
async function sendMissing(session, file) {
  let missing = session.missing_chunks || [];
  while (missing.length > 0) {
    const ordinal = missing[0];
    const offset = ordinal * session.chunk_bytes;
    const slice = file.slice(offset, offset + session.chunk_bytes);

    say(`Sending ${missing.length} of ${Math.ceil(file.size / session.chunk_bytes)} pieces…`);
    const updated = await send(
      "PUT",
      `/api/v1/uploads/${session.id}/chunks/${offset}`,
      slice,
      "application/octet-stream",
    );
    missing = updated.missing_chunks || [];
  }
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();

  const file = form.file.files[0];
  if (!file) return;

  const format = formatOf(file.name);
  if (!format) {
    say("That is not an EPUB, PDF, M4B or MP3.");
    return;
  }

  const button = form.querySelector("button");
  button.disabled = true;

  try {
    say("Hashing…");
    const session = await send(
      "POST",
      "/api/v1/uploads",
      JSON.stringify({
        source_id: Number(form.source_id.value),
        filename: file.name,
        format: format,
        total_bytes: file.size,
        sha256: await sha256(file),
      }),
      "application/json",
    );

    await sendMissing(session, file);

    say("Finishing…");
    await send("POST", `/api/v1/uploads/${session.id}/finalize`, null);

    say(`${file.name} added. A scan is queued to index it.`);
    form.reset();
  } catch (err) {
    say(err.message);
  } finally {
    button.disabled = false;
  }
});
