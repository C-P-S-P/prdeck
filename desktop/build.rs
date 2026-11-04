//! Builds the Go core (`prdeck`) next to the app, so `cargo run` starts a matching
//! `prdeck serve` without anything installed on PATH.
use std::{env, path::PathBuf, process::Command};

fn main() {
    let manifest = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let root = manifest.parent().unwrap();
    for p in ["cmd", "internal", "go.mod", "go.sum"] {
        println!("cargo:rerun-if-changed={}", root.join(p).display());
    }
    println!("cargo:rerun-if-env-changed=PRDECK_SKIP_GO");
    let target_os = env::var("CARGO_CFG_TARGET_OS").unwrap_or_default();
    let exe = if target_os == "windows" { "prdeck.exe" } else { "prdeck" };
    let out = PathBuf::from(env::var("OUT_DIR").unwrap()).join(exe);
    if env::var_os("PRDECK_SKIP_GO").is_some() {
        println!("cargo:rustc-env=PRDECK_SIDECAR=");
        return;
    }
    let mut go = Command::new("go");
    go.args(["build", "-o"]).arg(&out).arg("./cmd/prdeck").current_dir(root);
    // Cross-compiling (say for Windows from a Mac): build the core for the same system.
    if env::var("TARGET").ok() != env::var("HOST").ok() {
        let goos = match target_os.as_str() {
            "macos" => "darwin",
            os => os,
        };
        let goarch = match env::var("CARGO_CFG_TARGET_ARCH").unwrap_or_default().as_str() {
            "x86_64" => "amd64",
            "aarch64" => "arm64",
            arch => arch,
        }
        .to_string();
        go.env("GOOS", goos).env("GOARCH", goarch);
    }
    match go.status() {
        Ok(s) if s.success() => println!("cargo:rustc-env=PRDECK_SIDECAR={}", out.display()),
        _ => {
            println!("cargo:warning=could not build the Go core with `go build`; the app will look for prdeck on PATH or in $PRDECK_BIN");
            println!("cargo:rustc-env=PRDECK_SIDECAR=");
        }
    }
}
