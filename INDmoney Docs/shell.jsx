/* global React */
// ============================================================
// SHELL — top-level controller that switches between
//   (a) the Atlas (knowledge graph) view, and
//   (b) the LeafCanvas (Figma-like board) view for a single sub-flow
// ============================================================

const { useState, useCallback, useEffect } = window.React;

window.AtlasShell = function AtlasShell() {
  // open leaf id (if not null, leaf canvas is showing)
  const [leafId, setLeafId] = useState(null);
  const [selectedFrameId, setSelectedFrameId] = useState(null);

  const openLeaf = useCallback((id) => {
    setSelectedFrameId(null);
    setLeafId(id);
  }, []);
  const closeLeaf = useCallback(() => {
    setLeafId(null);
    setSelectedFrameId(null);
  }, []);

  useEffect(() => {
    const fn = (e) => {
      if (e.key === "Escape" && leafId) {
        if (selectedFrameId) setSelectedFrameId(null);
        else closeLeaf();
      }
    };
    window.addEventListener("keydown", fn);
    return () => window.removeEventListener("keydown", fn);
  }, [leafId, selectedFrameId, closeLeaf]);

  // Expose openLeaf so the AtlasApp inspector can call it
  useEffect(() => {
    window.__openLeaf = openLeaf;
    return () => { delete window.__openLeaf; };
  }, [openLeaf]);

  // Expose leaf-open state so Atlas's Escape handler doesn't fight ours
  useEffect(() => {
    window.__leafOpen = !!leafId;
    return () => { window.__leafOpen = false; };
  }, [leafId]);

  const leaf = leafId ? window.LEAVES.find(l => l.id === leafId) : null;

  return (
    <>
      <window.AtlasApp />
      {leaf && (
        <>
          <window.LeafCanvas
            leaf={leaf}
            onClose={closeLeaf}
            onPickFrame={setSelectedFrameId}
            selectedFrameId={selectedFrameId}
          />
          <window.LeafInspector
            leaf={leaf}
            frameId={selectedFrameId}
            onClose={closeLeaf}
            onPickFrame={setSelectedFrameId}
          />
        </>
      )}
    </>
  );
};
