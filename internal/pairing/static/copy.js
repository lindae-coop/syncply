// Botón «Copiar» do texto de vinculación. Sen este script, o texto pódese
// seleccionar e copiar a man.
for (const btn of document.querySelectorAll("button[data-copy]")) {
  const src = document.getElementById(btn.dataset.copy);
  const done = document.getElementById(btn.dataset.copy + "-copied");
  btn.hidden = false;
  btn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(src.value.trim());
    } catch {
      src.select();
      document.execCommand("copy");
    }
    if (done) {
      done.hidden = false;
      setTimeout(() => { done.hidden = true; }, 3000);
    }
  });
}
for (const ta of document.querySelectorAll("textarea[readonly]")) {
  ta.addEventListener("focus", () => ta.select());
}
