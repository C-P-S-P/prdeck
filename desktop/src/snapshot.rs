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
        cx.background_executor().timer(Duration::from_secs(3)).await;
        let _ = cx.update_window(window, |_, window, cx| deck.update(cx, |d, cx| d.show_state(&state, window, cx)));
        cx.background_executor().timer(Duration::from_millis(2500)).await;
        let _ = cx.update_window(window, |_, window, cx| {
            // Draw now: a window that is hidden or on another Space gets no frames.
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
