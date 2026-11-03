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
    let out = PathBuf::from(env::var("OUT_DIR").unwrap()).join("prdeck");
    if env::var_os("PRDECK_SKIP_GO").is_some() {
        println!("cargo:rustc-env=PRDECK_SIDECAR=");
        return;
    }
    let status = Command::new("go")
        .args(["build", "-o"])
        .arg(&out)
        .arg("./cmd/prdeck")
        .current_dir(root)
        .status();
    match status {
        Ok(s) if s.success() => println!("cargo:rustc-env=PRDECK_SIDECAR={}", out.display()),
        _ => {
            println!("cargo:warning=could not build the Go core with `go build`; the app will look for prdeck on PATH or in $PRDECK_BIN");
            println!("cargo:rustc-env=PRDECK_SIDECAR=");
        }
    }
}
