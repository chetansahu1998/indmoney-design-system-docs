/* global React */
// ============================================================
// LEAF CANVAS — Figma-like infinite board for a single sub-flow.
// Renders an array of "frames" (phone-mockup screens) on a
// pannable / zoomable canvas with curved connectors and a
// frame-counter overlay. Click a frame to open the inspector
// pinned to it; click empty canvas to deselect.
// ============================================================
const { useEffect, useRef, useState, useMemo, useCallback } = window.React;

// frame width/height (matches leaves.jsx FW/FH)
const FW = 280, FH = 580;

window.LeafCanvas = function LeafCanvas({ leaf, onClose, onPickFrame, selectedFrameId }) {
  const layout = useMemo(() => window.buildLeafCanvas(leaf), [leaf.id]);
  const stageRef = useRef(null);

  // ---- camera (pan + zoom)
  const [cam, setCam] = useState({ x: 0, y: 0, z: 0.6 });
  const camRef = useRef(cam);
  useEffect(() => { camRef.current = cam; }, [cam]);

  // Auto-fit to layout on mount
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    const rect = stage.getBoundingClientRect();
    const minX = Math.min(...layout.frames.map(f => f.x));
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w));
    const minY = Math.min(...layout.frames.map(f => f.y));
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h));
    const w = maxX - minX, h = maxY - minY;
    const padding = 120;
    const zx = (rect.width - 380 - padding * 2) / w;   // leave room for inspector
    const zy = (rect.height - 100 - padding * 2) / h;
    const z = Math.max(0.25, Math.min(1.0, Math.min(zx, zy)));
    const cx = (minX + maxX) / 2;
    const cy = (minY + maxY) / 2;
    setCam({ x: cx, y: cy, z });
  }, [leaf.id]);

  // ---- pan/zoom event handlers
  const dragRef = useRef({ active: false, startX: 0, startY: 0, camX: 0, camY: 0, moved: false });
  const onPointerDown = (e) => {
    if (e.target.closest(".lc-frame")) return; // let frame click bubble
    dragRef.current = {
      active: true,
      startX: e.clientX, startY: e.clientY,
      camX: camRef.current.x, camY: camRef.current.y,
      moved: false,
    };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  };
  const onPointerMove = (e) => {
    if (!dragRef.current.active) return;
    const dx = e.clientX - dragRef.current.startX;
    const dy = e.clientY - dragRef.current.startY;
    if (Math.hypot(dx, dy) > 3) dragRef.current.moved = true;
    const z = camRef.current.z;
    setCam(c => ({ ...c, x: dragRef.current.camX - dx / z, y: dragRef.current.camY - dy / z }));
  };
  const onPointerUp = (e) => {
    const wasMoved = dragRef.current.moved;
    dragRef.current.active = false;
    try { e.currentTarget.releasePointerCapture?.(e.pointerId); } catch {}
    if (!wasMoved && !e.target.closest(".lc-frame")) {
      onPickFrame?.(null);
    }
  };
  const onWheel = (e) => {
    e.preventDefault();
    const stage = stageRef.current;
    const rect = stage.getBoundingClientRect();
    const sx = e.clientX - rect.left;
    const sy = e.clientY - rect.top;
    const c = camRef.current;

    // ---- Trackpad-friendly wheel routing ------------------------------
    // Browsers send 3 different "wheel" event shapes; we route them:
    //
    //   (1) Pinch-to-zoom on a trackpad → wheel with ctrlKey=true,
    //       small deltaY (synthetic ctrl, not a real Ctrl press).
    //   (2) Two-finger SCROLL on a trackpad → wheel with non-zero deltaX
    //       and/or small fractional deltaY. We treat this as PAN.
    //   (3) Mouse wheel (line-mode) → deltaMode === 1 OR large integer
    //       deltaY with deltaX === 0. We treat this as ZOOM.
    //   (4) User-held Cmd/Meta + scroll → ZOOM (explicit).
    //
    // The detection: ctrlKey OR metaKey OR (deltaX === 0 AND deltaY is a
    // large integer with no x-component) → zoom. Everything else → pan.
    // ------------------------------------------------------------------
    const isPinch = e.ctrlKey; // trackpad pinch sets ctrlKey
    const isCmdZoom = e.metaKey;
    const looksLikeMouseWheel =
      e.deltaMode === 1 || // line mode
      (e.deltaX === 0 && Math.abs(e.deltaY) >= 50 && Number.isInteger(e.deltaY));
    const shouldZoom = isPinch || isCmdZoom || looksLikeMouseWheel;

    if (shouldZoom) {
      // world point under cursor
      const wx = c.x + (sx - rect.width / 2) / c.z;
      const wy = c.y + (sy - rect.height / 2) / c.z;
      // smoother continuous zoom: exponential of -deltaY scaled small for trackpad
      // pinch (which fires many small events) and large for mouse wheel.
      const k = isPinch ? 0.01 : isCmdZoom ? 0.005 : 0.002;
      const factor = Math.exp(-e.deltaY * k);
      const z = Math.max(0.18, Math.min(2.0, c.z * factor));
      const nx = wx - (sx - rect.width / 2) / z;
      const ny = wy - (sy - rect.height / 2) / z;
      setCam({ x: nx, y: ny, z });
    } else {
      // Two-finger PAN — translate camera in world space.
      // Shift+wheel on a mouse swaps axes (browser convention) — already
      // reflected in deltaX, so we just consume both axes directly.
      setCam({ x: c.x + e.deltaX / c.z, y: c.y + e.deltaY / c.z, z: c.z });
    }
  };
  useEffect(() => {
    const stage = stageRef.current;
    if (!stage) return;
    stage.addEventListener("wheel", onWheel, { passive: false });
    return () => stage.removeEventListener("wheel", onWheel);
  }, []);

  // ---- transform applied to "world" group: scale, then offset by -cam.
  // The .lc-world element is positioned at left:50%; top:50% in CSS,
  // which gives us the canvas-center origin we need.
  const transform = `scale(${cam.z}) translate(${-cam.x}px, ${-cam.y}px)`;

  // ---- connectors (SVG) — drawn in world space, so put SVG at world coords
  // Compute bounding world box for SVG
  const worldBounds = useMemo(() => {
    const minX = Math.min(...layout.frames.map(f => f.x)) - 200;
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w)) + 200;
    const minY = Math.min(...layout.frames.map(f => f.y)) - 200;
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h)) + 200;
    return { minX, minY, w: maxX - minX, h: maxY - minY };
  }, [layout]);

  const violations = useMemo(() => window.buildViolations(leaf), [leaf.id]);
  // group violations by frameIdx for badges
  const violationsByFrame = useMemo(() => {
    const m = {};
    violations.forEach(v => {
      (m[v.frameIdx] ??= []).push(v);
    });
    return m;
  }, [violations]);

  // ---- helpers
  const fitAll = () => {
    const stage = stageRef.current;
    const rect = stage.getBoundingClientRect();
    const minX = Math.min(...layout.frames.map(f => f.x));
    const maxX = Math.max(...layout.frames.map(f => f.x + f.w));
    const minY = Math.min(...layout.frames.map(f => f.y));
    const maxY = Math.max(...layout.frames.map(f => f.y + f.h));
    const w = maxX - minX, h = maxY - minY;
    const padding = 120;
    const zx = (rect.width - 380 - padding * 2) / w;
    const zy = (rect.height - 100 - padding * 2) / h;
    const z = Math.max(0.25, Math.min(1.0, Math.min(zx, zy)));
    setCam({ x: (minX + maxX) / 2, y: (minY + maxY) / 2, z });
  };
  const zoomIn = () => setCam(c => ({ ...c, z: Math.min(2.0, c.z * 1.25) }));
  const zoomOut = () => setCam(c => ({ ...c, z: Math.max(0.18, c.z / 1.25) }));
  const focusOnFrame = (id) => {
    const f = layout.frames.find(x => x.id === id);
    if (!f) return;
    setCam({ x: f.x + f.w / 2, y: f.y + f.h / 2, z: 0.85 });
  };

  // ---- when a frame becomes selected externally, focus on it
  useEffect(() => {
    if (selectedFrameId) focusOnFrame(selectedFrameId);
  }, [selectedFrameId]);

  return (
    <div className="leafcanvas">
      <LeafTopBar leaf={leaf} onClose={onClose} onPickLeaf={(id) => { window.__openLeaf?.(id); }} violations={violations.length} cam={cam} />
      <div
        className="lc-stage"
        ref={stageRef}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        style={{
          backgroundImage:
            "radial-gradient(rgba(255,255,255,0.045) 1px, transparent 1px)",
          backgroundSize: `${24 * cam.z}px ${24 * cam.z}px`,
          backgroundPosition: `calc(50% - ${cam.x * cam.z}px) calc(50% - ${cam.y * cam.z}px)`,
        }}
      >
        <div className="lc-world" style={{ transform, transformOrigin: "0 0" }}>
          {/* SVG connectors layer */}
          <svg
            className="lc-edges"
            style={{
              position: "absolute",
              left: worldBounds.minX,
              top: worldBounds.minY,
              width: worldBounds.w,
              height: worldBounds.h,
              pointerEvents: "none",
              overflow: "visible",
            }}
            viewBox={`${worldBounds.minX} ${worldBounds.minY} ${worldBounds.w} ${worldBounds.h}`}
            preserveAspectRatio="none"
          >
            <defs>
              <marker id="lc-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                <path d="M0 0 L10 5 L0 10 z" fill="rgba(126,184,255,0.7)" />
              </marker>
              <marker id="lc-arrow-back" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto">
                <path d="M0 0 L10 5 L0 10 z" fill="rgba(255,180,120,0.7)" />
              </marker>
            </defs>
            {layout.edges.map((e, i) => {
              const A = layout.frames.find(f => f.id === e.from);
              const B = layout.frames.find(f => f.id === e.to);
              if (!A || !B) return null;
              const ax = A.x + A.w, ay = A.y + A.h / 2;
              const bx = B.x,        by = B.y + B.h / 2;
              const dx = bx - ax;
              // gentle horizontal cubic
              const c1x = ax + Math.abs(dx) * 0.45;
              const c2x = bx - Math.abs(dx) * 0.45;
              const path = `M ${ax} ${ay} C ${c1x} ${ay}, ${c2x} ${by}, ${bx} ${by}`;
              const isBack = e.kind === "back";
              const stroke = isBack ? "rgba(255,180,120,0.55)" : "rgba(126,184,255,0.55)";
              const dasharray = isBack ? "6 4" : "none";
              return (
                <path
                  key={i}
                  d={path}
                  fill="none"
                  stroke={stroke}
                  strokeWidth="1.6"
                  strokeDasharray={dasharray}
                  markerEnd={isBack ? "url(#lc-arrow-back)" : "url(#lc-arrow)"}
                />
              );
            })}
          </svg>

          {/* Frames */}
          {layout.frames.map((f) => {
            const fv = violationsByFrame[f.idx] || [];
            const isSel = selectedFrameId === f.id;
            return (
              <div
                key={f.id}
                className={`lc-frame ${isSel ? "is-sel" : ""}`}
                style={{ left: f.x, top: f.y, width: f.w, height: f.h }}
                onClick={(e) => { e.stopPropagation(); onPickFrame?.(f.id); }}
              >
                <div className="lc-frame-tab">
                  <span className="lc-frame-num">{String(f.idx + 1).padStart(2, "0")}</span>
                  <span className="lc-frame-name">{f.label}</span>
                  {fv.length > 0 && (
                    <span className={`lc-frame-badge sev-${
                      fv.some(v => v.severity === "error") ? "error"
                      : fv.some(v => v.severity === "warning") ? "warning"
                      : "info"
                    }`}>{fv.length}</span>
                  )}
                </div>
                <div className="lc-frame-body">
                  <window.PhoneFrame kind={f.kind} idx={f.idx} label={f.label} total={layout.frames.length} />
                </div>
                {/* violation pins inside the frame */}
                <div className="lc-pins">
                  {fv.slice(0, 4).map((v, i) => (
                    <span
                      key={v.id}
                      className={`lc-pin sev-${v.severity}`}
                      style={{
                        left: 30 + (i % 2) * 180,
                        top: 80 + Math.floor(i / 2) * 220,
                      }}
                      title={`${v.rule}\n${v.layer}`}
                    >{i + 1}</span>
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* Bottom-left zoom & nav */}
      <div className="lc-zoom">
        <button onClick={zoomOut} title="Zoom out">−</button>
        <button className="lc-zoom-num" onClick={fitAll} title="Fit to canvas">{Math.round(cam.z * 100)}%</button>
        <button onClick={zoomIn} title="Zoom in">+</button>
        <span className="lc-zoom-sep" />
        <button onClick={fitAll} title="Fit all">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>
        </button>
      </div>

      {/* Frame strip — gives an overview & quick jump */}
      <div className="lc-strip">
        {layout.frames.map((f, i) => {
          const fv = violationsByFrame[f.idx] || [];
          return (
            <button
              key={f.id}
              className={`lc-strip-cell ${selectedFrameId === f.id ? "is-sel" : ""}`}
              onClick={() => onPickFrame?.(f.id)}
              title={f.label}
            >
              <span className="lc-strip-num">{String(i + 1).padStart(2, "0")}</span>
              <span className="lc-strip-label">{f.label}</span>
              {fv.length > 0 && (
                <span className={`lc-strip-dot sev-${
                  fv.some(v => v.severity === "error") ? "error"
                  : fv.some(v => v.severity === "warning") ? "warning"
                  : "info"
                }`} />
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
};

// ============================================================
// LeafTopBar — sticky header for the leaf canvas
//   - Back button → returns to Atlas (preserves selection)
//   - Flow name dropdown → jump to ANY flow's first sub-flow
//   - Sub-flow name dropdown → jump to a sibling sub-flow
//   - Prev / Next arrows → cycle through siblings
//   - Frames + violations stats on the right
// ============================================================
function LeafTopBar({ leaf, onClose, onPickLeaf, violations, cam }) {
  const flow = window.FLOWS_BY_ID?.[leaf.flow];
  const allLeaves = window.LEAVES || [];
  // siblings = sub-flows under the same parent flow
  const siblings = useMemo(() => allLeaves.filter(l => l.flow === leaf.flow), [leaf.flow]);
  const sibIdx = siblings.findIndex(l => l.id === leaf.id);

  const [flowMenu, setFlowMenu] = useState(false);
  const [subMenu, setSubMenu] = useState(false);

  // Close menus on outside click / esc
  useEffect(() => {
    if (!flowMenu && !subMenu) return;
    const onDown = (e) => {
      if (!e.target.closest?.(".lc-menu") && !e.target.closest?.(".lc-crumb-btn")) {
        setFlowMenu(false); setSubMenu(false);
      }
    };
    const onKey = (e) => { if (e.key === "Escape") { setFlowMenu(false); setSubMenu(false); } };
    window.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey, true);
    };
  }, [flowMenu, subMenu]);

  // Group all leaves by flow for the flow-picker menu
  const grouped = useMemo(() => {
    const m = new Map();
    for (const l of allLeaves) {
      if (!m.has(l.flow)) m.set(l.flow, []);
      m.get(l.flow).push(l);
    }
    return [...m.entries()];
  }, []);

  const goSibling = (delta) => {
    const next = siblings[(sibIdx + delta + siblings.length) % siblings.length];
    if (next && next.id !== leaf.id) onPickLeaf(next.id);
  };

  return (
    <div className="lc-top">
      <button className="lc-back" onClick={onClose} title="Back to Atlas (Esc)">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M19 12H5M12 19l-7-7 7-7"/></svg>
        <span>Back to Atlas</span>
      </button>

      <div className="lc-top-title">
        <div className="lc-top-eyebrow">
          {/* Flow dropdown */}
          <button
            className="lc-crumb-btn"
            onClick={(e) => { e.stopPropagation(); setFlowMenu(v => !v); setSubMenu(false); }}
          >
            {flow?.label || leaf.flow}
            <svg className="lc-caret" width="10" height="10" viewBox="0 0 12 12"><path d="M2 4l4 4 4-4" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>
          {flowMenu && (
            <div className="lc-menu lc-menu-flows">
              <div className="lc-menu-head">Jump to flow</div>
              {grouped.map(([flowId, leaves]) => {
                const f = window.FLOWS_BY_ID?.[flowId];
                return (
                  <div key={flowId} className="lc-menu-group">
                    <div className="lc-menu-group-label">{f?.label || flowId}</div>
                    {leaves.map(l => (
                      <button
                        key={l.id}
                        className={`lc-menu-item ${l.id === leaf.id ? "is-current" : ""}`}
                        onClick={() => { setFlowMenu(false); if (l.id !== leaf.id) onPickLeaf(l.id); }}
                      >
                        <span className="lc-menu-item-label">{l.label}</span>
                        <span className="lc-menu-item-meta">
                          {l.frames}
                          {l.violations > 0 && <span className="lc-menu-item-warn"> · {l.violations}</span>}
                        </span>
                      </button>
                    ))}
                  </div>
                );
              })}
            </div>
          )}
          <span className="lc-top-sep">›</span>
          <span className="lc-crumb-static">Sub-flow</span>
        </div>

        <div className="lc-top-name-row">
          {/* Prev sibling */}
          <button
            className="lc-sib-arrow"
            onClick={() => goSibling(-1)}
            title="Previous sub-flow"
            disabled={siblings.length < 2}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M15 18l-6-6 6-6"/></svg>
          </button>

          {/* Sub-flow name dropdown */}
          <button
            className="lc-top-name lc-crumb-btn"
            onClick={(e) => { e.stopPropagation(); setSubMenu(v => !v); setFlowMenu(false); }}
          >
            {leaf.label}
            <svg className="lc-caret-lg" width="12" height="12" viewBox="0 0 12 12"><path d="M2 4l4 4 4-4" stroke="currentColor" strokeWidth="1.6" fill="none" strokeLinecap="round" strokeLinejoin="round"/></svg>
          </button>

          {/* Next sibling */}
          <button
            className="lc-sib-arrow"
            onClick={() => goSibling(1)}
            title="Next sub-flow"
            disabled={siblings.length < 2}
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M9 18l6-6-6-6"/></svg>
          </button>

          <span className="lc-sib-pos">{sibIdx + 1} / {siblings.length}</span>

          {subMenu && (
            <div className="lc-menu lc-menu-subs">
              <div className="lc-menu-head">{flow?.label || leaf.flow} · sub-flows</div>
              {siblings.map(l => (
                <button
                  key={l.id}
                  className={`lc-menu-item ${l.id === leaf.id ? "is-current" : ""}`}
                  onClick={() => { setSubMenu(false); if (l.id !== leaf.id) onPickLeaf(l.id); }}
                >
                  <span className="lc-menu-item-label">{l.label}</span>
                  <span className="lc-menu-item-meta">
                    {l.frames}
                    {l.violations > 0 && <span className="lc-menu-item-warn"> · {l.violations}</span>}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <div className="lc-top-meta">
        <div className="lc-top-stat">
          <span className="lc-top-stat-num">{leaf.frames}</span>
          <span className="lc-top-stat-lbl">frames</span>
        </div>
        <div className="lc-top-stat">
          <span className={`lc-top-stat-num ${violations > 0 ? "is-warn" : ""}`}>{violations}</span>
          <span className="lc-top-stat-lbl">violations</span>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// LeafInspector — DRD / decisions / violations / activity tabs
// ============================================================
window.LeafInspector = function LeafInspector({ leaf, frameId, onClose, onPickFrame }) {
  const [tab, setTab] = useState("drd");
  const violations = useMemo(() => window.buildViolations(leaf), [leaf.id]);
  const decisions = useMemo(() => window.buildDecisions(leaf), [leaf.id]);
  const activity = useMemo(() => window.buildActivity(leaf), [leaf.id]);
  const comments = useMemo(() => window.buildComments(leaf), [leaf.id]);

  const frame = frameId
    ? window.buildLeafCanvas(leaf).frames.find(f => f.id === frameId)
    : null;

  return (
    <div className="lc-ins">
      <div className="lc-ins-head">
        <div>
          <div className="lc-ins-eyebrow">{frame ? "Frame" : "Sub-flow"}</div>
          <div className="lc-ins-name">{frame ? frame.label : leaf.label}</div>
          {frame && <div className="lc-ins-meta">Frame {frame.idx + 1} of {leaf.frames} · {leaf.label}</div>}
          {!frame && <div className="lc-ins-meta">{leaf.frames} frames · {violations.length} violations · {decisions.length} decisions</div>}
        </div>
        <button className="lc-ins-close" onClick={onClose}>✕</button>
      </div>
      <div className="lc-ins-tabs">
        {["drd", "violations", "decisions", "activity", "comments"].map(t => (
          <button
            key={t}
            className={`lc-ins-tab ${tab === t ? "is-active" : ""}`}
            onClick={() => setTab(t)}
          >
            {t === "drd" ? "DRD" : t.charAt(0).toUpperCase() + t.slice(1)}
            {t === "violations" && violations.length > 0 && (
              <span className="lc-tab-pill">{violations.length}</span>
            )}
            {t === "decisions" && decisions.length > 0 && (
              <span className="lc-tab-pill">{decisions.length}</span>
            )}
          </button>
        ))}
      </div>
      <div className="lc-ins-body">
        {tab === "drd" && <DRDTab leaf={leaf} frame={frame} />}
        {tab === "violations" && (
          <ViolationsTab
            violations={frame ? violations.filter(v => v.frameIdx === frame.idx) : violations}
            onPickFrame={onPickFrame}
            leaf={leaf}
          />
        )}
        {tab === "decisions" && <DecisionsTab decisions={decisions} leaf={leaf} onPickFrame={onPickFrame} />}
        {tab === "activity" && <ActivityTab activity={activity} />}
        {tab === "comments" && <CommentsTab comments={comments} />}
      </div>
    </div>
  );
};

function DRDTab({ leaf, frame }) {
  // DRD = Design Requirement Doc
  return (
    <div className="lc-drd">
      <div className="lc-drd-section">
        <div className="lc-drd-h">Purpose</div>
        <p>
          {frame
            ? `This frame handles the "${frame.label}" step of the ${leaf.label} flow.`
            : `${leaf.label} establishes user identity, fetches authoritative records, and gates downstream investing actions.`}
        </p>
      </div>
      <div className="lc-drd-section">
        <div className="lc-drd-h">Entry & exit</div>
        <ul className="lc-drd-ul">
          <li><b>Enters from:</b> Onboarding hub, deeplinks from "complete KYC" banners</li>
          <li><b>Exits to:</b> Next sub-flow in onboarding chain</li>
          <li><b>Drop-out:</b> 8.4% (industry benchmark 12%)</li>
        </ul>
      </div>
      <div className="lc-drd-section">
        <div className="lc-drd-h">Acceptance criteria</div>
        <ol className="lc-drd-ol">
          <li>All form fields validate on blur, not on submit.</li>
          <li>API failures surface a retry CTA within 4 seconds.</li>
          <li>All copy strings sourced from <code>i18n/onboarding.yml</code>.</li>
          <li>Tap targets ≥ 44pt; AA contrast on all text.</li>
        </ol>
      </div>
      <div className="lc-drd-section">
        <div className="lc-drd-h">Open questions</div>
        <ul className="lc-drd-ul lc-drd-q">
          <li>Should we show partial save state if user backgrounds the app?</li>
          <li>Confirm legal copy with @rohit before launch.</li>
        </ul>
      </div>
      <div className="lc-drd-section">
        <div className="lc-drd-h">References</div>
        <div className="lc-link-list">
          <a className="lc-link">📄 PRD · Onboarding KYC v3</a>
          <a className="lc-link">🎨 Figma · KYC frames</a>
          <a className="lc-link">📊 Mixpanel · funnel</a>
          <a className="lc-link">🔗 Jira · KYC-2841</a>
        </div>
      </div>
    </div>
  );
}

function ViolationsTab({ violations, onPickFrame, leaf }) {
  const [filter, setFilter] = useState("active");
  const filtered = violations.filter(v => filter === "all" || v.status === filter);
  const layout = useMemo(() => window.buildLeafCanvas(leaf), [leaf.id]);
  if (violations.length === 0) {
    return (
      <div className="lc-empty">
        <div className="lc-empty-icon">✓</div>
        <div className="lc-empty-h">No violations</div>
        <div className="lc-empty-sub">This sub-flow passes all design system checks.</div>
      </div>
    );
  }
  return (
    <div className="lc-vio">
      <div className="lc-vio-filter">
        {["active", "acknowledged", "fixed", "all"].map(s => (
          <button key={s} className={`lc-vio-fil ${filter === s ? "is-active" : ""}`} onClick={() => setFilter(s)}>
            {s} {s !== "all" && <span className="lc-vio-fil-num">{violations.filter(v => v.status === s).length}</span>}
          </button>
        ))}
      </div>
      <div className="lc-vio-list">
        {filtered.map(v => {
          const f = layout.frames.find(fr => fr.idx === v.frameIdx);
          return (
            <div key={v.id} className={`lc-vio-row sev-${v.severity}`}>
              <div className="lc-vio-row-head">
                <span className={`lc-vio-sev sev-${v.severity}`}>
                  {v.severity === "error" ? "✕" : v.severity === "warning" ? "!" : "i"}
                </span>
                <span className="lc-vio-rule">{v.rule}</span>
                <span className="lc-vio-ago">{v.ago}</span>
              </div>
              <div className="lc-vio-detail">{v.detail}</div>
              <div className="lc-vio-meta">
                <button className="lc-vio-frameref" onClick={() => onPickFrame?.(f?.id)}>
                  → Frame {v.frameIdx + 1} · {f?.label}
                </button>
                <span className="lc-vio-layer">{v.layer}</span>
                <span className={`lc-vio-status status-${v.status}`}>{v.status}</span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function DecisionsTab({ decisions, leaf, onPickFrame }) {
  const layout = useMemo(() => window.buildLeafCanvas(leaf), [leaf.id]);
  if (decisions.length === 0) {
    return <div className="lc-empty"><div className="lc-empty-h">No decisions logged</div></div>;
  }
  return (
    <div className="lc-dec">
      {decisions.map(d => {
        const f = d.linksTo != null ? layout.frames[d.linksTo] : null;
        return (
          <div key={d.id} className="lc-dec-row">
            <div className="lc-dec-marker" />
            <div className="lc-dec-content">
              <div className="lc-dec-title">{d.title}</div>
              <div className="lc-dec-body">{d.body}</div>
              <div className="lc-dec-foot">
                <span>{d.author}</span>
                <span className="lc-dec-dot">·</span>
                <span>{d.ago}</span>
                {f && (
                  <>
                    <span className="lc-dec-dot">·</span>
                    <button className="lc-vio-frameref" onClick={() => onPickFrame?.(f.id)}>
                      → Frame {d.linksTo + 1}
                    </button>
                  </>
                )}
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function ActivityTab({ activity }) {
  return (
    <div className="lc-act">
      {activity.map((a, i) => (
        <div key={i} className="lc-act-row">
          <div className={`lc-act-icon kind-${a.kind}`} />
          <div className="lc-act-body">
            <div><b>{a.who}</b> {a.what}</div>
            <div className="lc-act-ago">{a.ago}</div>
          </div>
        </div>
      ))}
    </div>
  );
}

function CommentsTab({ comments }) {
  return (
    <div className="lc-com">
      {comments.map((c, i) => (
        <div key={i} className="lc-com-row">
          <div className="lc-com-avatar" style={{ background: `hsl(${(i + 1) * 73}, 30%, 60%)` }}>{c.who[0]}</div>
          <div className="lc-com-body">
            <div className="lc-com-head"><b>{c.who}</b><span className="lc-com-ago">{c.ago}</span></div>
            <div className="lc-com-text">{c.body}</div>
            {c.reactions > 0 && <div className="lc-com-react">👍 {c.reactions}</div>}
          </div>
        </div>
      ))}
      <div className="lc-com-input">
        <div className="lc-com-input-field">Reply…</div>
      </div>
    </div>
  );
}
