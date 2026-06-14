const grid = document.getElementById("catalog-grid");

function humanSize(bytes) {
  if (!bytes && bytes !== 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let n = bytes, i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(n < 10 && i > 0 ? 1 : 0)} ${units[i]}`;
}

function formatDate(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
}

function card(p, index) {
  const el = document.createElement("article");
  el.className = "card";
  el.style.animationDelay = `${Math.min(index * 60, 400)}ms`;

  const tags = (p.tags || []).map((t) => `<span class="tag">${t}</span>`).join("");
  const meta = [formatDate(p.updated), humanSize(p.size)].filter(Boolean).join(" · ");

  el.innerHTML = `
    <img class="card__cover" src="${p.image}" alt="${p.title} cover" loading="lazy">
    <div class="card__body">
      <h3 class="card__title">${p.title}</h3>
      <p class="card__desc">${p.description || ""}</p>
      ${tags ? `<div class="card__tags">${tags}</div>` : ""}
      <div class="card__foot">
        <span class="card__meta">${meta}</span>
        <a class="download" href="${p.download}" download>Download</a>
      </div>
    </div>`;
  return el;
}

async function load() {
  try {
    const res = await fetch("manifest.json", { cache: "no-cache" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    const items = (data.presentations || []).slice().sort((a, b) => {
      return new Date(b.updated || 0) - new Date(a.updated || 0);
    });

    grid.innerHTML = "";
    if (items.length === 0) {
      grid.innerHTML = `<p class="grid__state">No presentations yet. Check back soon.</p>`;
      return;
    }
    items.forEach((p, i) => grid.appendChild(card(p, i)));
  } catch (err) {
    grid.innerHTML = `<p class="grid__state">Could not load the catalog (${err.message}).</p>`;
  }
}

load();
