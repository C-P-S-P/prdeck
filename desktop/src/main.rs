//! PR Deck for the desktop: a GPUI window (the UI framework Zed is built on) over
//! prdeck's Go core, which runs as `prdeck serve` in the background.
//!
//! Run with sample data: `cargo run -- --demo`

mod deck;
mod diff;
mod layout;
mod markdown;
mod model;
mod rpc;
#[cfg(feature = "snapshot")]
mod snapshot;
mod theme;

use std::borrow::Cow;

use gpui_kit::*;

use deck::*;

gpui_kit::actions!(prdeck, [Quit, Hide, HideOthers, ShowAll, Minimize, Zoom]);

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

/// The standard Mac shortcuts, plus PR Deck's own. Deck-level ones only fire while
/// the PR Deck window has focus.
fn bind_keys(cx: &mut App) {
    cx.bind_keys([
        KeyBinding::new("cmd-q", Quit, None),
        KeyBinding::new("cmd-h", Hide, None),
        KeyBinding::new("alt-cmd-h", HideOthers, None),
        KeyBinding::new("cmd-m", Minimize, None),
        KeyBinding::new("cmd-t", GoToPr, Some("Deck")),
        KeyBinding::new("cmd-p", GoToPr, Some("Deck")),
        KeyBinding::new("cmd-k", Filters, Some("Deck")),
        KeyBinding::new("cmd-w", CloseTab, Some("Deck")),
        KeyBinding::new("cmd-shift-t", ReopenTab, Some("Deck")),
        KeyBinding::new("cmd-r", Refresh, Some("Deck")),
        KeyBinding::new("cmd-shift-]", NextTab, Some("Deck")),
        KeyBinding::new("cmd-shift-[", PrevTab, Some("Deck")),
        KeyBinding::new("ctrl-tab", NextTab, Some("Deck")),
        KeyBinding::new("ctrl-shift-tab", PrevTab, Some("Deck")),
        KeyBinding::new("cmd-,", OpenConfig, Some("Deck")),
        KeyBinding::new("cmd-o", OpenInEditor, Some("Deck")),
        KeyBinding::new("cmd-shift-o", OpenOnGitHub, Some("Deck")),
        KeyBinding::new("cmd-b", ToggleSidebar, Some("Deck")),
        KeyBinding::new("cmd-shift-c", CommentOnPr, Some("Deck")),
        KeyBinding::new("cmd-shift-l", ChangeClone, Some("Deck")),
    ]);
}

fn menus() -> Vec<Menu> {
    vec![
        Menu {
            name: "PR Deck".into(),
            items: vec![
                MenuItem::action("Set Up PR Deck…", RunSetup),
                MenuItem::action("Settings…", OpenConfig),
                MenuItem::separator(),
                MenuItem::action("Hide PR Deck", Hide),
                MenuItem::action("Hide Others", HideOthers),
                MenuItem::action("Show All", ShowAll),
                MenuItem::separator(),
                MenuItem::action("Quit PR Deck", Quit),
            ],
            disabled: false,
        },
        Menu {
            name: "File".into(),
            items: vec![
                MenuItem::action("Go to Pull Request…", GoToPr),
                MenuItem::action("Reopen Closed Tab", ReopenTab),
                MenuItem::action("Close Tab", CloseTab),
                MenuItem::separator(),
                MenuItem::action("Open in Editor", OpenInEditor),
                MenuItem::action("Open on GitHub", OpenOnGitHub),
                MenuItem::action("Use Another Clone…", ChangeClone),
                MenuItem::separator(),
                MenuItem::action("Comment or Review…", CommentOnPr),
            ],
            disabled: false,
        },
        Menu {
            name: "View".into(),
            items: vec![
                MenuItem::action("Filters…", Filters),
                MenuItem::action("Refresh", Refresh),
                MenuItem::action("Toggle Sidebar", ToggleSidebar),
                MenuItem::separator(),
                MenuItem::action("Next Tab", NextTab),
                MenuItem::action("Previous Tab", PrevTab),
            ],
            disabled: false,
        },
        Menu {
            name: "Window".into(),
            items: vec![MenuItem::action("Minimize", Minimize), MenuItem::action("Zoom", Zoom)],
            disabled: false,
        },
    ]
}

fn main() {
    let demo = std::env::args().any(|a| a == "--demo");
    gpui_kit::application().with_assets(AppAssets).run(move |cx| {
        gpui_kit::init(cx);
        bind_keys(cx);
        cx.on_action(|_: &Quit, cx| cx.quit());
        cx.on_action(|_: &Hide, cx| cx.hide());
        cx.on_action(|_: &HideOthers, cx| cx.hide_other_apps());
        cx.on_action(|_: &ShowAll, cx| cx.unhide_other_apps());
        cx.on_action(|_: &Minimize, cx| {
            if let Some(w) = cx.active_window() {
                let _ = w.update(cx, |_, window, _| window.minimize_window());
            }
        });
        cx.on_action(|_: &Zoom, cx| {
            if let Some(w) = cx.active_window() {
                let _ = w.update(cx, |_, window, _| window.zoom_window());
            }
        });
        cx.set_menus(menus());

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
            cx.new(|cx| Deck::new(demo, window, cx))
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
