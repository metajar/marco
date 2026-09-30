(function () {
  "use strict";

  var charts = [];
  var STATE_COLORS = { "On Wi-Fi": "#2fb344", "Off Wi-Fi": "#d63939", "Not tracked": "#adb5bd" };

  function isDark() {
    return document.documentElement.getAttribute("data-bs-theme") === "dark";
  }

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function fmtDur(ms) {
    var m = Math.round(ms / 60000);
    if (m < 1) return "under a minute";
    if (m < 60) return m + "m";
    var h = Math.floor(m / 60);
    m = m % 60;
    if (h < 24) return h + "h" + (m ? " " + m + "m" : "");
    var d = Math.floor(h / 24);
    h = h % 24;
    return d + "d" + (h ? " " + h + "h" : "");
  }

  function fmtTime(ms) {
    return new Date(ms).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" });
  }

  function base(type, height) {
    var dark = isDark();
    return {
      chart: {
        type: type,
        height: height,
        fontFamily: "inherit",
        background: "transparent",
        foreColor: dark ? "#9aa0ac" : "#6c7a91",
        toolbar: { show: false },
        zoom: { enabled: false },
        animations: { enabled: false },
        parentHeightOffset: 0,
      },
      theme: { mode: dark ? "dark" : "light" },
      grid: { strokeDashArray: 4, borderColor: dark ? "rgba(255,255,255,.08)" : "rgba(4,32,69,.1)" },
      dataLabels: { enabled: false },
    };
  }

  function render(el, opts) {
    var chart = new ApexCharts(el, opts);
    chart.render();
    charts.push(chart);
  }

  // Presence timeline: one row per device, green/red/grey spans over time,
  // with optional shaded bands showing schedule windows.
  function timeline(sel, series, from, to, bands) {
    var el = document.querySelector(sel);
    if (!el || !window.ApexCharts) return;
    series = (series || []).filter(function (s) { return s.data && s.data.length; });
    if (!series.length) {
      el.innerHTML = '<div class="text-secondary">No history yet — check back after Marco has been running for a bit.</div>';
      return;
    }
    var rows = parseInt(el.getAttribute("data-rows") || "1", 10);
    var opts = base("rangeBar", Math.max(130, rows * 46 + 70));
    opts.series = series;
    opts.colors = series.map(function (s) { return STATE_COLORS[s.name] || "#066fd1"; });
    opts.plotOptions = { bar: { horizontal: true, barHeight: "60%", rangeBarGroupRows: true, borderRadius: 0 } };
    opts.xaxis = { type: "datetime", min: from, max: to, labels: { datetimeUTC: false } };
    opts.yaxis = { labels: { maxWidth: 200 } };
    opts.legend = { show: false };
    opts.fill = { opacity: 1 };
    opts.stroke = { width: 0 };
    opts.tooltip = {
      custom: function (ctx) {
        var s = ctx.w.config.series[ctx.seriesIndex];
        var p = s.data[ctx.dataPointIndex];
        return '<div class="p-2 small"><strong>' + esc(p.x) + "</strong><br>" +
          '<span style="color:' + (STATE_COLORS[s.name] || "inherit") + '">●</span> ' + esc(s.name) + "<br>" +
          fmtTime(p.y[0]) + " → " + fmtTime(p.y[1]) + "<br>" +
          '<span class="text-secondary">' + fmtDur(p.y[1] - p.y[0]) + "</span></div>";
      },
    };
    opts.annotations = {
      xaxis: (bands || []).map(function (b) {
        return { x: b[0], x2: b[1], fillColor: "#4299e1", opacity: 0.15, borderColor: "transparent" };
      }),
    };
    render(el, opts);
  }

  // Stacked/grouped bars of hours per day.
  function bars(sel, data, o) {
    var el = document.querySelector(sel);
    if (!el || !window.ApexCharts || !data) return;
    o = o || {};
    var opts = base("bar", o.height || 280);
    opts.chart.stacked = !!o.stacked;
    opts.series = data.series || [];
    opts.xaxis = { categories: data.categories || [], labels: { rotate: -45, hideOverlappingLabels: true } };
    opts.yaxis = { labels: { formatter: function (v) { return (Math.round(v * 10) / 10) + (o.unit || ""); } } };
    opts.legend = { position: "top", horizontalAlign: "left" };
    opts.plotOptions = { bar: { columnWidth: "60%", borderRadius: 2 } };
    opts.tooltip = { y: { formatter: function (v) { return fmtDur(v * 3600000); } } };
    if (o.colors) opts.colors = o.colors;
    render(el, opts);
  }

  function setTheme(mode) {
    document.documentElement.setAttribute("data-bs-theme", mode);
    try { localStorage.setItem("marco-theme", mode); } catch (e) {}
    charts.forEach(function (c) {
      c.updateOptions({
        theme: { mode: mode },
        chart: { foreColor: mode === "dark" ? "#9aa0ac" : "#6c7a91" },
        grid: { borderColor: mode === "dark" ? "rgba(255,255,255,.08)" : "rgba(4,32,69,.1)" },
      });
    });
  }

  document.addEventListener("click", function (e) {
    var t = e.target.closest(".js-theme");
    if (t) {
      e.preventDefault();
      setTheme(isDark() ? "light" : "dark");
      return;
    }
    var reveal = e.target.closest(".js-reveal");
    if (reveal) {
      e.preventDefault();
      var input = document.getElementById(reveal.getAttribute("data-target"));
      if (input) input.type = input.type === "password" ? "text" : "password";
      return;
    }
    var preset = e.target.closest("[data-preset]");
    if (preset) {
      var p = JSON.parse(preset.getAttribute("data-preset"));
      var form = document.getElementById("schedule-form");
      form.querySelector("[name=name]").value = p.name;
      form.querySelector("[name=start]").value = p.start;
      form.querySelector("[name=end]").value = p.end;
      form.querySelectorAll("[name=days]").forEach(function (cb) {
        cb.checked = p.days.indexOf(parseInt(cb.value, 10)) >= 0;
      });
    }
  });

  document.addEventListener("submit", function (e) {
    var form = e.target;
    var msg = form.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) {
      e.preventDefault();
      return;
    }
    var back = form.querySelector(".js-back");
    if (back) {
      var input = form.querySelector("[name=back]");
      if (input && !input.value) input.value = location.pathname + location.search;
    }
    if (form.id === "scan-form") {
      var btn = form.querySelector("button");
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner-border spinner-border-sm me-2"></span>Scanning… (about 10 seconds)';
    }
  });

  // Pages that show live status refresh themselves unless you're busy on them.
  var auto = document.querySelector("[data-autorefresh]");
  if (auto) {
    var secs = Math.max(30, parseInt(auto.getAttribute("data-autorefresh"), 10) || 60);
    var tick = function () {
      var busy = document.hidden ||
        document.querySelector(".dropdown-menu.show") ||
        (document.activeElement && /INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName));
      if (busy) {
        setTimeout(tick, 5000);
      } else {
        location.reload();
      }
    };
    setTimeout(tick, secs * 1000);
  }

  window.Marco = { timeline: timeline, bars: bars };
})();
