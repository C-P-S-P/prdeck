//! PR Deck for the desktop: a GPUI window (the UI framework Zed is built on) over
//! prdeck's Go core, which runs as `prdeck serve` in the background.
//!
//! Run with sample data: `cargo run -- --demo`

mod deck;
#[cfg(feature = "snapshot")]
mod snapshot;
mod model;
mod rpc;
mod theme;

use std::borrow::Cow;

use gpui_kit::*;

gpui_kit::actions!(prdeck, [Quit]);

/// The app's own icons, then the ones bundled with GPUI Kit.
struct AppAssets;

const ICONS: &[(&str, &[u8])] = &[
    ("icons/git-pull-request.svg", include_bytes!("../assets/icons/git-pull-request.svg")),
    ("icons/git-pull-request-draft.svg", include_bytes!("../assets/icons/git-pull-request-draft.svg")),
    ("icons/git-branch.svg", include_bytes!("../assets/icons/git-branch.svg")),
    ("icons/message-square.svg", include_bytes!("../assets/icons/message-square.svg")),
    ("icons/git-commit-horizontal.svg", include_bytes!("../assets/icons/git-commit-horizontal.svg")),
    ("icons/file-diff.svg", include_bytes!("../assets/icons/file-diff.svg")),
    ("icons/folder-git-2.svg", include_bytes!("../assets/icons/folder-git-2.svg")),
];

impl AssetSource for AppAssets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        if let Some((_, b)) = ICONS.iter().find(|(p, _)| *p == path) {
            return Ok(Some(Cow::Borrowed(*b)));
        }
        gpui_kit::assets::Assets.load(path)
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        gpui_kit::assets::Assets.list(path)
    }
}

fn main() {
    let demo = std::env::args().any(|a| a == "--demo");
    gpui_kit::application().with_assets(AppAssets).run(move |cx| {
        gpui_kit::init(cx);
        cx.bind_keys([KeyBinding::new("cmd-q", Quit, None)]);
        cx.on_action(|_: &Quit, cx| cx.quit());
        cx.set_menus(vec![Menu {
            name: "PR Deck".into(),
            items: vec![MenuItem::action("Quit PR Deck", Quit)],
            disabled: false,
        }]);

        let bounds = Bounds::centered(None, size(px(1360.), px(880.)), cx);
        let options = WindowOptions {
            window_bounds: Some(WindowBounds::Windowed(bounds)),
            titlebar: Some(TitlebarOptions {
                title: Some("PR Deck".into()),
                appears_transparent: true,
                traffic_light_position: Some(point(px(14.), px(13.))),
            }),
            window_min_size: Some(size(px(960.), px(600.))),
            ..Default::default()
        };
        let (_window, _deck) = gpui_kit::open_window(options, cx, |window, cx| {
            theme::apply(window, cx);
            cx.new(|cx| deck::Deck::new(demo, window, cx))
        })
        .expect("could not open the PR Deck window");
        #[cfg(feature = "snapshot")]
        snapshot::schedule(_window, _deck, cx);
        cx.on_window_closed(|cx, _| {
            if cx.windows().is_empty() {
                cx.quit();
            }
        })
        .detach();
        cx.activate(true);
    });
}
