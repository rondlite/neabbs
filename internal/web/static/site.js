(function () {
  "use strict";
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---- language toggle: swaps data-nl/data-en text, persists choice ---- */
  function applyLang(lang) {
    document.querySelectorAll("[data-nl]").forEach(function (el) {
      el.textContent = el.getAttribute(lang === "en" ? "data-en" : "data-nl");
    });
    document.documentElement.lang = lang;
    document.getElementById("lang-nl").setAttribute("aria-pressed", String(lang === "nl"));
    document.getElementById("lang-en").setAttribute("aria-pressed", String(lang === "en"));
    try { localStorage.setItem("neabbs-lang", lang); } catch (e) { /* private mode */ }
  }
  var saved = "nl";
  try { saved = localStorage.getItem("neabbs-lang") || "nl"; } catch (e) { /* private mode */ }
  applyLang(saved === "en" ? "en" : "nl");
  document.getElementById("lang-nl").addEventListener("click", function () { applyLang("nl"); });
  document.getElementById("lang-en").addEventListener("click", function () { applyLang("en"); });

  /* ---- boot sequence (in-character, Dutch always) ---- */
  var LOGO = [
    " _   _ _____    _    ____  ____ ____",
    "| \\ | | ____|  / \\  | __ )| __ ) ___|",
    "|  \\| |  _|   / _ \\ |  _ \\|  _ \\___ \\",
    "| |\\  | |___ / ___ \\| |_) | |_) |__) |",
    "|_| \\_|_____/_/   \\_\\____/|____/____/"
  ];
  var bootLines = ["ATDT 020-621984", "CONNECT 1200", ""]
    .concat(LOGO)
    .concat(["", "Heropend na 40 jaar stilte.", ""]);

  var out = document.getElementById("boot-out");
  var prompt = document.getElementById("prompt");

  function showStats() {
    fetch("/api/status").then(function (r) { return r.json(); }).then(function (s) {
      var line = "er zijn nu " + s.callers_online + " bellers online · " +
        s.registered + " geregistreerde bellers\n";
      out.textContent += line;
    }).catch(function () { /* stats offline: hero stays as-is */ });
  }

  function finishBoot() {
    prompt.hidden = false;
    showStats();
  }

  if (reduced) {
    out.textContent = bootLines.join("\n") + "\n";
    finishBoot();
  } else {
    var li = 0, ci = 0;
    (function type() {
      if (li >= bootLines.length) { finishBoot(); return; }
      var line = bootLines[li];
      if (ci < line.length) {
        out.textContent += line.charAt(ci++);
        setTimeout(type, LOGO.indexOf(line) >= 0 ? 2 : 24);
      } else {
        out.textContent += "\n";
        li++; ci = 0;
        setTimeout(type, li === 2 ? 500 : 90);
      }
    })();
  }

  /* ---- the glitch row: flickers in for ~200ms, never explained ---- */
  var glitch = document.getElementById("glitch-row");
  if (glitch && !reduced) {
    (function scheduleGlitch() {
      setTimeout(function () {
        glitch.hidden = false;
        setTimeout(function () {
          glitch.hidden = true;
          scheduleGlitch();
        }, 200);
      }, 8000 + Math.random() * 14000);
    })();
  }
})();
