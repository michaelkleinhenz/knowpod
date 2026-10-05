import { HTMLAttributes, MouseEvent, ReactNode, TouchEvent, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

export interface MenuItem {
  label: string;
  onSelect: () => void;
  // danger marks an item that deletes something.
  danger?: boolean;
}

// LONG_PRESS is how long a finger rests on an item to open its menu on touch screens.
const LONG_PRESS = 500;
// MOVE_LIMIT is how far (in px) the finger may move before it is a scroll, not a long press.
const MOVE_LIMIT = 10;

// useContextMenu makes a menu for an item that opens on right click or long press. Spread
// handlers on the item and render menu next to it.
export function useContextMenu(items: MenuItem[]): { handlers: Pick<HTMLAttributes<HTMLElement>, 'onContextMenu' | 'onTouchStart' | 'onTouchMove' | 'onTouchEnd' | 'onTouchCancel' | 'onClickCapture'>; menu: ReactNode } {
  const [at, setAt] = useState<{ x: number; y: number } | null>(null);
  const timer = useRef<number>(0);
  const start = useRef({ x: 0, y: 0 });
  const fired = useRef(false);

  const cancel = () => window.clearTimeout(timer.current);
  useEffect(() => cancel, []);

  const handlers = {
    onContextMenu: (e: MouseEvent) => {
      if (items.length === 0) return;
      e.preventDefault();
      e.stopPropagation();
      cancel();
      // From the keyboard (the menu key or Shift+F10) there's no pointer: it opens below the item.
      if (e.clientX === 0 && e.clientY === 0) {
        const r = e.currentTarget.getBoundingClientRect();
        setAt({ x: r.left + 8, y: r.bottom });
      } else setAt({ x: e.clientX, y: e.clientY });
    },
    onTouchStart: (e: TouchEvent) => {
      if (items.length === 0 || e.touches.length !== 1) return;
      const { clientX: x, clientY: y } = e.touches[0];
      start.current = { x, y };
      fired.current = false;
      cancel();
      timer.current = window.setTimeout(() => {
        fired.current = true;
        setAt({ x, y });
      }, LONG_PRESS);
    },
    onTouchMove: (e: TouchEvent) => {
      const t = e.touches[0];
      if (t && Math.hypot(t.clientX - start.current.x, t.clientY - start.current.y) > MOVE_LIMIT) cancel();
    },
    onTouchEnd: cancel,
    onTouchCancel: cancel,
    // The tap that ends a long press doesn't open the item.
    onClickCapture: (e: MouseEvent) => {
      if (fired.current) {
        fired.current = false;
        e.preventDefault();
        e.stopPropagation();
      }
    },
  };

  const menu = at && items.length > 0 ? <Menu at={at} items={items} onClose={() => setAt(null)} /> : null;
  return { handlers, menu };
}

function Menu({ at, items, onClose }: { at: { x: number; y: number }; items: MenuItem[]; onClose: () => void }) {
  const ref = useRef<HTMLUListElement>(null);
  const [pos, setPos] = useState(at);

  // Kept inside the window, and focused for the keyboard.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setPos({ x: Math.max(4, Math.min(at.x, window.innerWidth - r.width - 4)), y: Math.max(4, Math.min(at.y, window.innerHeight - r.height - 4)) });
    el.querySelector<HTMLElement>('button')?.focus();
  }, [at]);

  useEffect(() => {
    const away = (e: Event) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    const key = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    document.addEventListener('pointerdown', away, true);
    document.addEventListener('keydown', key);
    window.addEventListener('scroll', onClose, true);
    window.addEventListener('resize', onClose);
    return () => {
      document.removeEventListener('pointerdown', away, true);
      document.removeEventListener('keydown', key);
      window.removeEventListener('scroll', onClose, true);
      window.removeEventListener('resize', onClose);
    };
  }, [onClose]);

  return createPortal(
    <ul ref={ref} className="context-menu" role="menu" style={{ left: pos.x, top: pos.y }}>
      {items.map((it) => (
        <li key={it.label} role="none">
          <button
            type="button"
            role="menuitem"
            className={it.danger ? 'danger' : undefined}
            onClick={() => {
              onClose();
              it.onSelect();
            }}
          >
            {it.label}
          </button>
        </li>
      ))}
    </ul>,
    document.body,
  );
}

// MenuDiv is a div with a context menu (right click or long press).
export function MenuDiv({ items, children, ...props }: { items: MenuItem[] } & HTMLAttributes<HTMLDivElement>) {
  const { handlers, menu } = useContextMenu(items);
  return (
    <div {...props} {...handlers}>
      {children}
      {menu}
    </div>
  );
}
