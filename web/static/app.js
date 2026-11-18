// Minimal progressive enhancement: responsive Masonry sizing, HLS/MP4 video controls,
// history-aware back button, and share helper. All primary navigation uses static HTML links.

function copyTextToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopyText(text));
  }
  return Promise.resolve(fallbackCopyText(text));
}

function fallbackCopyText(text) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.top = "-9999px";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  document.body.removeChild(ta);
  return ok;
}

function handleShare(event, btnElement) {
  event.preventDefault();
  event.stopPropagation();
  const btn = btnElement || event.currentTarget;
  const rawURL = btn?.dataset?.shareUrl || "";
  const fullURL = rawURL ? new URL(rawURL, window.location.origin).href : window.location.href;
  const title = btn?.dataset?.shareTitle || document.title;

  copyTextToClipboard(fullURL).then((copied) => {
    if (copied && btn) {
      const prevColor = btn.style.color;
      const prevTitle = btn.title;
      btn.style.color = "#4caf50";
      btn.title = "Copied link!";
      setTimeout(() => {
        btn.style.color = prevColor;
        btn.title = prevTitle;
      }, 1500);
      return;
    }
    if (navigator.share) {
      navigator.share({ url: fullURL, title }).catch(() => {});
    }
  });
}

function toggleMute(btn) {
  const container = btn.closest(".video-container");
  const video = container?.querySelector("video");
  if (!video) return;
  const isMuted = !video.muted;
  document.querySelectorAll("video").forEach((v) => {
    v.muted = isMuted;
    updateMuteUI(v.closest(".video-container"), isMuted);
  });
  try {
    localStorage.setItem("videoMuted", String(isMuted));
  } catch {
    // Ignore storage restriction.
  }
}

function updateMuteUI(container, isMuted) {
  const btn = container?.querySelector(".mute-toggle");
  if (!btn) return;
  btn.innerHTML = isMuted
    ? '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M16.5 12c0-1.77-1.02-3.29-2.5-4.03v2.21l2.45 2.45c.03-.2.05-.41.05-.63zm2.5 0c0 .94-.2 1.82-.54 2.64l1.51 1.51C20.63 14.91 21 13.5 21 12c0-4.28-2.99-7.86-7-8.77v2.06c2.89.86 5 3.54 5 6.71zM4.27 3L3 4.27 7.73 9H3v6h4l5 5v-6.73l4.25 4.25c-.58.45-1.24.8-1.95.98v2.06c1.26-.26 2.39-.81 3.37-1.56l2.3 2.3 1.27-1.27L4.27 3zM10 15.17L7.83 13H5v-2h2.83l.88-.88L10 11.41v3.76zM12 4L9.91 6.09 12 8.18V4z"/></svg>'
    : '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M3 9v6h4l5 5V4L7 9H3zm13.5 3c0-1.77-1.02-3.29-2.5-4.03v8.05c1.48-.73 2.5-2.25 2.5-4.02zM14 3.23v2.06c2.89.86 5 3.54 5 6.71s-2.11 5.85-5 6.71v2.06c4.01-.91 7-4.49 7-8.77s-2.99-7.86-7-8.77z"/></svg>';
}

function toggleFullscreen(btn) {
  const container = btn.closest(".video-container");
  const video = container?.querySelector("video");
  if (!container) return;
  if (!document.fullscreenElement) {
    if (container.requestFullscreen) container.requestFullscreen();
    else if (container.webkitRequestFullscreen) container.webkitRequestFullscreen();
    else if (video?.webkitEnterFullscreen) video.webkitEnterFullscreen();
  } else if (document.exitFullscreen) {
    document.exitFullscreen();
  }
}

document.addEventListener("DOMContentLoaded", () => {
  // Standard back navigation: use browser history when the user navigated here,
  // falling back to the static href when opened directly in a fresh tab.
  document.querySelectorAll(".back-link").forEach((backLink) => {
    if (window.history.length > 1) {
      backLink.addEventListener("click", (e) => {
        e.preventDefault();
        window.history.back();
      });
    }
  });

  // Loading skeletons: tint with validated dominant color, track img + video poster completion,
  // and recover aborted loads when returning via back/forward cache (pageshow).
  const hexColorRe = /^#[0-9a-f]{3,8}$/i;
  let isPageHidden = false;

  function retryImageLoad(img, box) {
    const rawSrc = img.getAttribute("src");
    if (!rawSrc || img._retriedThisShow) return false;
    img._retriedThisShow = true;
    img._allowRestoreRetry = false;
    box.classList.remove("is-loaded");
    img.removeAttribute("src");
    window.requestAnimationFrame(() => {
      img.setAttribute("src", rawSrc);
    });
    return true;
  }

  function syncMediaSkeletons(isRestore) {
    document.querySelectorAll(".post-media, .pin-media-frame, .board-thumbnail").forEach((box) => {
      if (!box.dataset.colorApplied) {
        box.dataset.colorApplied = "1";
        const color = box.getAttribute("data-color");
        if (color && hexColorRe.test(color)) {
          box.style.setProperty("--skeleton-bg", color);
        }
      }

      const img = box.querySelector("img");
      if (img) {
        if (!img.dataset.skeletonWired) {
          img.dataset.skeletonWired = "1";
          img.addEventListener("load", () => {
            if (img.naturalWidth > 0) {
              img.dataset.terminalError = "";
              img._allowRestoreRetry = false;
              box.classList.add("is-loaded");
              if (typeof window.recalculateMasonry === "function") {
                window.recalculateMasonry();
              }
            }
          });
          img.addEventListener("error", () => {
            if (isPageHidden) return;
            if (img._allowRestoreRetry && retryImageLoad(img, box)) {
              return;
            }
            img.dataset.terminalError = "1";
            box.classList.add("is-loaded");
          });
        }

        if (isRestore && img.dataset.terminalError !== "1") {
          img._retriedThisShow = false;
        }

        if (img.complete && img.naturalWidth > 0) {
          img.dataset.terminalError = "";
          img._allowRestoreRetry = false;
          box.classList.add("is-loaded");
        } else if (img.complete && img.naturalWidth === 0) {
          if (img.dataset.terminalError === "1" || !retryImageLoad(img, box)) {
            img.dataset.terminalError = "1";
            box.classList.add("is-loaded");
          }
        } else {
          box.classList.remove("is-loaded");
          if (isRestore && img.dataset.terminalError !== "1") {
            img._allowRestoreRetry = true;
          }
        }
        return;
      }

      const video = box.querySelector("video");
      if (video) {
        const poster = video.getAttribute("poster");
        if (!poster) {
          box.classList.add("is-loaded");
          return;
        }
        if (!box.dataset.posterVerified && (!box.classList.contains("is-loaded") || isRestore)) {
          box.classList.remove("is-loaded");
          const probe = new Image();
          probe.onload = () => {
            box.dataset.posterVerified = "1";
            box.classList.add("is-loaded");
          };
          probe.onerror = () => {
            if (!isPageHidden) {
              box.classList.add("is-loaded");
            }
          };
          probe.src = poster;
          if (probe.complete && probe.naturalWidth > 0) {
            box.dataset.posterVerified = "1";
            box.classList.add("is-loaded");
          }
        }
        return;
      }

      box.classList.add("is-loaded");
    });
  }

  syncMediaSkeletons(false);
  window.addEventListener("pagehide", () => {
    isPageHidden = true;
  });
  window.addEventListener("pageshow", (event) => {
    isPageHidden = false;
    syncMediaSkeletons(Boolean(event.persisted));
  });
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      isPageHidden = false;
      syncMediaSkeletons(false);
    }
  });

  // Responsive Masonry sizing
  const masonryInstances = [];
  if (typeof Masonry !== "undefined") {
    const mobile = window.innerWidth <= 620;
    document.querySelectorAll(".grid").forEach((grid) => {
      const sizer = grid.querySelector(".grid-sizer");
      grid.classList.add("masonry-ready");
      masonryInstances.push(new Masonry(grid, {
        itemSelector: ".grid-item",
        columnWidth: sizer || 240,
        gutter: 16,
        fitWidth: !mobile,
        transitionDuration: 0,
      }));
    });
    document.querySelectorAll(".boards-grid").forEach((grid) => {
      grid.classList.add("masonry-ready");
      masonryInstances.push(new Masonry(grid, {
        itemSelector: ".board-card",
        columnWidth: mobile ? ".grid-sizer" : 280,
        gutter: mobile ? 16 : 20,
        fitWidth: !mobile,
        transitionDuration: 0,
      }));
    });
  }

  function updateShowMoreLabels() {
    document.querySelectorAll(".post-description").forEach((desc) => {
      const label = desc.nextElementSibling;
      if (label && label.classList.contains("show-more-label")) {
        label.style.display = desc.scrollHeight > desc.clientHeight + 1 ? "inline-block" : "none";
      }
    });
  }

  let layoutRaf = 0;
  window.recalculateMasonry = () => {
    if (layoutRaf) return;
    layoutRaf = window.requestAnimationFrame(() => {
      layoutRaf = 0;
      updateShowMoreLabels();
      masonryInstances.forEach((m) => m.layout());
    });
  };

  document.addEventListener("change", (e) => {
    if (e.target.classList?.contains("desc-toggle")) {
      setTimeout(window.recalculateMasonry, 10);
    }
  });
  window.addEventListener("resize", () => window.recalculateMasonry());
  updateShowMoreLabels();

  // Video players (HLS via hls.light.min.js + native MP4 fallback)
  let globalMuted = true;
  try {
    globalMuted = localStorage.getItem("videoMuted") !== "false";
  } catch {
    globalMuted = true;
  }

  function initVideo(container) {
    if (container.dataset.initialized) return;
    container.dataset.initialized = "true";

    const video = container.querySelector("video");
    const progressBar = container.querySelector(".video-progress-bar");
    const progressContainer = container.querySelector(".video-progress-container");
    const src = video?.getAttribute("data-src");
    const mp4Fallback = video?.getAttribute("data-mp4");
    if (!video || !src) return;

    video.muted = globalMuted;
    updateMuteUI(container, globalMuted);

    const isM3U8 = src.includes(".m3u8");
    const autoLoad = container.dataset.autoload === "true";
    container._wantPlay = false;
    let mediaAttached = false;
    let triedMP4Fallback = false;

    const fallbackToMP4 = () => {
      if (!mp4Fallback || triedMP4Fallback) return false;
      triedMP4Fallback = true;
      if (container._hls) {
        container._hls.destroy();
        container._hls = null;
      }
      video.src = mp4Fallback;
      video.load();
      if (container._wantPlay) {
        video.play().catch(() => {});
      }
      return true;
    };

    const attachMedia = () => {
      if (mediaAttached) return;
      mediaAttached = true;

      if (isM3U8) {
        if (typeof Hls !== "undefined" && Hls.isSupported()) {
          const hls = new Hls({
            capLevelToPlayerSize: true,
            autoStartLoad: true,
          });
          container._hls = hls;
          hls.on(Hls.Events.MANIFEST_PARSED, () => {
            if (container._wantPlay && video.paused) {
              video.play().catch(() => {});
            }
          });
          hls.on(Hls.Events.ERROR, (_event, data) => {
            if (
              data?.fatal ||
              data?.details === "internalException" ||
              data?.details === "manifestLoadError" ||
              data?.details === "manifestParsingError"
            ) {
              if (!fallbackToMP4()) {
                container.classList.remove("loading");
                if (container._wantPlay) {
                  container._wantPlay = false;
                  container.classList.add("paused");
                }
              }
            }
          });
          hls.loadSource(src);
          hls.attachMedia(video);
          return;
        }
        if (video.canPlayType("application/vnd.apple.mpegurl")) {
          video.src = src;
          if (autoLoad) video.preload = "auto";
          return;
        }
        if (mp4Fallback) {
          triedMP4Fallback = true;
          video.src = mp4Fallback;
          return;
        }
      }
      video.src = src;
    };

    if (autoLoad) {
      attachMedia();
    }

    container.addEventListener("click", (e) => {
      if (e.target.closest(".mute-toggle") || e.target.closest(".fullscreen-toggle") || e.target.closest(".open-pin-link")) {
        return;
      }
      if (video.paused && !container._wantPlay) {
        container._wantPlay = true;
        document.querySelectorAll(".video-container").forEach((otherContainer) => {
          if (otherContainer === container) return;
          otherContainer._wantPlay = false;
          const otherVideo = otherContainer.querySelector("video");
          if (otherVideo && !otherVideo.paused) {
            otherVideo.pause();
          }
          otherContainer.classList.add("paused");
          otherContainer.classList.remove("loading");
          if (otherContainer._hls) {
            otherContainer._hls.stopLoad();
          }
        });
        container.classList.remove("paused");
        if (video.readyState < 3) container.classList.add("loading");
        attachMedia();
        if (container._hls) {
          container._hls.startLoad();
        }
        video.play().catch(() => {});
      } else {
        container._wantPlay = false;
        video.pause();
        container.classList.add("paused");
        container.classList.remove("loading");
      }
    });

    video.addEventListener("loadedmetadata", () => {
      const frame = container.closest(".pin-media-frame");
      if (frame && !frame.dataset.aspectLocked && video.videoWidth > 0 && video.videoHeight > 0) {
        frame.dataset.aspectLocked = "1";
        frame.style.setProperty("--w", String(video.videoWidth));
        frame.style.setProperty("--h", String(video.videoHeight));
        frame.classList.add("has-aspect");
      }
    });
    video.addEventListener("waiting", () => container.classList.add("loading"));
    video.addEventListener("playing", () => {
      container.classList.remove("loading");
      container.classList.remove("paused");
    });
    video.addEventListener("canplay", () => {
      container.classList.remove("loading");
      if (container._wantPlay && video.paused) {
        video.play().catch(() => {});
      }
    });
    video.addEventListener("ended", () => {
      container._wantPlay = false;
      container.classList.add("paused");
      container.classList.remove("loading");
    });
    video.addEventListener("error", () => {
      if (!fallbackToMP4()) {
        container.classList.remove("loading");
        container._wantPlay = false;
        container.classList.add("paused");
      }
    });
    video.addEventListener("timeupdate", () => {
      if (progressBar && video.duration) {
        progressBar.style.width = `${(video.currentTime / video.duration) * 100}%`;
      }
    });
    if (progressContainer) {
      progressContainer.addEventListener("click", (e) => {
        e.stopPropagation();
        if (!video.duration) return;
        const rect = progressContainer.getBoundingClientRect();
        const pos = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
        video.currentTime = pos * video.duration;
      });
    }
  }

  // Scroll-responsive header: shorten on downward scroll, expand on upward scroll or near top.
  const header = document.querySelector(".search-header-container");
  if (header) {
    const TOP_EXPAND_Y = 36;
    const MIN_COLLAPSE_Y = 96;
    const DOWN_THRESHOLD = 20;
    const UP_THRESHOLD = 28;
    const JITTER_PX = 2;
    const COOLDOWN_MS = 260;

    let lastY = Math.max(0, window.scrollY || window.pageYOffset || 0);
    let accum = 0;
    let lastDir = 0;
    let isCompact = false;
    let cooldownUntil = 0;
    let ticking = false;

    const setCompact = (next) => {
      if (next === isCompact) return;
      isCompact = next;
      header.classList.toggle("header-compact", next);
      accum = 0;
      cooldownUntil = performance.now() + COOLDOWN_MS;
    };

    const updateHeaderOnScroll = () => {
      ticking = false;
      const y = Math.max(0, window.scrollY || window.pageYOffset || 0);
      const maxScroll = document.documentElement.scrollHeight - window.innerHeight;
      if (maxScroll < 220 || y <= TOP_EXPAND_Y) {
        setCompact(false);
        lastY = y;
        accum = 0;
        lastDir = 0;
        return;
      }

      const now = performance.now();
      if (now < cooldownUntil) {
        lastY = y;
        return;
      }

      const delta = y - lastY;
      lastY = y;
      if (Math.abs(delta) < JITTER_PX) return;

      const dir = delta > 0 ? 1 : -1;
      if (dir !== lastDir) {
        accum = 0;
        lastDir = dir;
      }
      accum += Math.abs(delta);

      if (dir === 1 && !isCompact && y >= MIN_COLLAPSE_Y && accum >= DOWN_THRESHOLD) {
        if (!header.contains(document.activeElement)) {
          setCompact(true);
        }
      } else if (dir === -1 && isCompact && accum >= UP_THRESHOLD) {
        setCompact(false);
      }
    };

    window.addEventListener("scroll", () => {
      if (!ticking) {
        ticking = true;
        window.requestAnimationFrame(updateHeaderOnScroll);
      }
    }, { passive: true });
  }

  const containers = document.querySelectorAll(".video-container");
  containers.forEach(initVideo);
  if (containers.length > 0 && "IntersectionObserver" in window) {
    const observer = new IntersectionObserver((entries) => {
      entries.forEach((entry) => {
        const c = entry.target;
        if (!entry.isIntersecting && c._hls && c.querySelector("video")?.paused) {
          c._hls.stopLoad();
        }
      });
    }, { rootMargin: "120px" });
    containers.forEach((c) => {
      if (c.dataset.autoload !== "true") {
        observer.observe(c);
      }
    });
  }
});
