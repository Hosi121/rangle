export function attachPlaybackGestures(element, toggle, now = () => performance.now()) {
  let start, lastTap, lastTouch = -Infinity, multiple = false;
  const pointers = new Set();
  const down = event => {
    pointers.add(event.pointerId);
    multiple ||= pointers.size > 1;
    start = { id: event.pointerId, x: event.clientX, y: event.clientY };
  };
  const up = event => {
    const tap = event.pointerType === 'touch' && !multiple && start?.id === event.pointerId &&
      Math.hypot(event.clientX - start.x, event.clientY - start.y) < 10;
    pointers.delete(event.pointerId);
    if (!pointers.size) multiple = false;
    if (!tap) { lastTap = undefined; return; }
    lastTouch = now();
    if (lastTap && lastTouch - lastTap.time < 300 &&
        Math.hypot(event.clientX - lastTap.x, event.clientY - lastTap.y) < 20) {
      event.preventDefault();
      toggle();
      lastTap = undefined;
    } else lastTap = { time: lastTouch, x: event.clientX, y: event.clientY };
  };
  const doubleClick = event => {
    if (now() - lastTouch < 500) return;
    event.preventDefault();
    toggle();
  };
  const cancel = () => { pointers.clear(); start = lastTap = undefined; multiple = false; };
  const handlers = { pointerdown: down, pointerup: up, pointercancel: cancel, dblclick: doubleClick };
  for (const [name, handler] of Object.entries(handlers)) element.addEventListener(name, handler);
  return () => {
    for (const [name, handler] of Object.entries(handlers)) element.removeEventListener(name, handler);
  };
}
