//! Saves the window as a PNG and quits (`--snapshot out.png [--state NAME]`), for the
//! README screenshots. Built only with `--features snapshot`.

use std::time::Duration;

use gpui_kit::*;

use crate::deck::Deck;

fn arg(name: &str) -> Option<String> {
    let args: Vec<String> = std::env::args().collect();
    args.iter().position(|a| a == name).and_then(|i| args.get(i + 1).cloned())
}

pub fn schedule(window: AnyWindowHandle, deck: Entity<Deck>, cx: &mut App) {
    let Some(path) = arg("--snapshot") else { return };
    let state = arg("--state").unwrap_or_default();
    cx.spawn(async move |cx| {
        cx.background_executor().timer(Duration::from_secs(arg("--wait").and_then(|w| w.parse().ok()).unwrap_or(3))).await;
        let steps: Vec<String> = state.split(',').map(String::from).collect();
        for st in steps {
            let st = st.as_str();
            if let Some(drag) = st.strip_prefix("drag:") {
                // drag:x1 y1 x2 y2 — press, move in steps, release.
                let v: Vec<f32> = drag.split(' ').filter_map(|n| n.parse().ok()).collect();
                if let [x1, y1, x2, y2] = v[..] {
                    let at = |t: f32| point(px(x1 + (x2 - x1) * t), px(y1 + (y2 - y1) * t));
                    let mut events = vec![
                        PlatformInput::MouseMove(MouseMoveEvent { position: at(0.), pressed_button: None, modifiers: Modifiers::default() }),
                        PlatformInput::MouseDown(MouseDownEvent {
                            button: MouseButton::Left,
                            position: at(0.),
                            modifiers: Modifiers::default(),
                            click_count: 1,
                            first_mouse: false,
                        }),
                    ];
                    for i in 1..=10 {
                        events.push(PlatformInput::MouseMove(MouseMoveEvent {
                            position: at(i as f32 / 10.),
                            pressed_button: Some(MouseButton::Left),
                            modifiers: Modifiers::default(),
                        }));
                    }
                    events.push(PlatformInput::MouseUp(MouseUpEvent {
                        button: MouseButton::Left,
                        position: at(1.),
                        modifiers: Modifiers::default(),
                        click_count: 1,
                    }));
                    for e in events {
                        let _ = cx.update_window(window, |_, window, cx| {
                            window.dispatch_event(e, cx);
                            let _ = window.draw(cx);
                        });
                        cx.background_executor().timer(Duration::from_millis(30)).await;
                    }
                }
                continue;
            }
            if let Some(keys) = st.strip_prefix("keys:") {
                let keys: Vec<String> = keys.split(' ').map(String::from).collect();
                // Real keystrokes through the keymap, reporting the window after each.
                for k in keys {
                    let ks = Keystroke::parse(Box::leak(k.clone().into_boxed_str())).ok();
                    let _ = cx.update_window(window, |_, window, cx| {
                        if let Some(ks) = ks {
                            window.dispatch_keystroke(ks, cx);
                        }
                    });
                    cx.background_executor().timer(Duration::from_millis(300)).await;
                    let _ = cx.update_window(window, |_, window, cx| {
                        let _ = window.draw(cx);
                        eprintln!("{k:>14} → {}", deck.read(cx).describe());
                    });
                }
                continue;
            }
            let _ = cx.update_window(window, |_, window, cx| deck.update(cx, |d, cx| d.show_state(st, window, cx)));
            let wait: u64 = arg("--wait").and_then(|w| w.parse().ok()).unwrap_or(1);
            for _ in 0..wait * 2 {
                cx.background_executor().timer(Duration::from_millis(500)).await;
                let _ = cx.update_window(window, |_, window, cx| {
                    let _ = window.draw(cx);
                });
            }
        }
        cx.background_executor().timer(Duration::from_millis(2500)).await;
        // Draw now (a window that is hidden or on another Space gets no frames), then
        // again once images that the first draw started decoding are ready.
        for _ in 0..3 {
            let _ = cx.update_window(window, |_, window, cx| {
                let _ = window.draw(cx);
            });
            cx.background_executor().timer(Duration::from_millis(1200)).await;
        }
        let _ = cx.update_window(window, |_, window, cx| {
            let _ = window.draw(cx);
        });
        let _ = cx.update_window(window, |_, window, _| match window.render_to_image() {
            Ok(img) => match img.save(&path) {
                Ok(()) => eprintln!("saved {path}"),
                Err(e) => eprintln!("saving {path}: {e}"),
            },
            Err(e) => eprintln!("render: {e}"),
        });
        let _ = cx.update(|cx| cx.quit());
    })
    .detach();
}
