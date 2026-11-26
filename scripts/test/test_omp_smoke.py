"""Checks for scripts/test/test-omp.sh."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "test" / "test-omp.sh"
SKIP_LINE = "RESULT skip: omp is not installed"


class OmpSmokeScriptTests(unittest.TestCase):
    def test_shell_syntax(self):
        bash = shutil.which("bash")
        self.assertIsNotNone(bash)
        result = subprocess.run(
            [bash, "-n", str(SCRIPT)], capture_output=True, text=True, timeout=30
        )
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)

    def test_herdr_without_omp_skips_cleanly(self):
        bash = shutil.which("bash")
        dirname_bin = shutil.which("dirname")
        self.assertIsNotNone(bash)
        self.assertIsNotNone(dirname_bin)
        with tempfile.TemporaryDirectory(prefix="omp-smoke-test-") as tmp:
            tools = Path(tmp) / "bin"
            tools.mkdir()
            os.symlink(dirname_bin, tools / "dirname")
            fake_herdr = tools / "herdr"
            fake_herdr.write_text("#!/bin/sh\nexit 0\n")
            fake_herdr.chmod(0o755)
            env = os.environ.copy()
            # Fully controlled PATH containing only tools (no /usr/bin or /bin where a host omp may live).
            env["PATH"] = str(tools)
            result = subprocess.run(
                [bash, str(SCRIPT)], cwd=str(ROOT), env=env,
                capture_output=True, text=True, timeout=30,
            )
        output = result.stdout + result.stderr
        self.assertEqual(0, result.returncode, output)
        lines = output.splitlines()
        self.assertEqual(1, lines.count(SKIP_LINE), output)
        self.assertEqual([SKIP_LINE], [line for line in lines if line.startswith("RESULT ")], output)

    def test_clears_inherited_herdr_socket_and_escalates_watchdog_to_sigkill(self):
        bash = shutil.which("bash")
        self.assertIsNotNone(bash)
        with tempfile.TemporaryDirectory(prefix="omp-smoke-watchdog-") as tmp:
            tmp_path = Path(tmp)
            tools = tmp_path / "bin"
            tools.mkdir()
            herdr_log = tmp_path / "herdr.log"
            work_record = tmp_path / "work_dir.txt"

            for util in ("dirname", "mktemp", "mkdir", "seq", "sleep", "grep", "tail", "sed", "rm", "cat", "chmod", "python3"):
                src = shutil.which(util)
                if src:
                    os.symlink(src, tools / util)

            fake_herdr = tools / "herdr"
            fake_herdr.write_text(
                "#!/bin/sh\n"
                f"echo \"ARGS=$* HERDR_SOCKET_PATH=${{HERDR_SOCKET_PATH:-<unset>}}\" >> \"{herdr_log}\"\n"
                "case \"$*\" in\n"
                "  *\"server stop\"*) exit 0 ;;\n"
                "  *\" server\"*)\n"
                "    python3 -c 'import os, socket, sys, time; p = os.path.join(os.environ[\"XDG_CONFIG_HOME\"], \"herdr\", \"sessions\", os.environ[\"HERDR_SESSION\"], \"herdr.sock\"); os.makedirs(os.path.dirname(p), exist_ok=True); s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.bind(p); s.listen(1); time.sleep(30)' &\n"
                "    wait $!\n"
                "    exit 0 ;;\n"
                "  *\"workspace create\"*)\n"
                "    echo '{\"result\":{\"root_pane\":{\"pane_id\":\"pane-1\"}}}'\n"
                "    exit 0 ;;\n"
                "  *\"pane run\"*) exit 0 ;;\n"
                "esac\n"
                "exit 0\n"
            )
            fake_herdr.chmod(0o755)

            fake_omp = tools / "omp"
            fake_omp.write_text("#!/bin/sh\nexit 0\n")
            fake_omp.chmod(0o755)

            fake_go = tools / "go"
            fake_go.write_text(
                "#!/bin/sh\n"
                "if [ \"$1\" = \"env\" ]; then echo \"/tmp/fake-go-$2\"; exit 0; fi\n"
                "out=\"\"\n"
                "while [ $# -gt 0 ]; do\n"
                "  if [ \"$1\" = \"-o\" ]; then out=\"$2\"; shift 2; continue; fi\n"
                "  shift\n"
                "done\n"
                "cat > \"$out\" <<'EOF'\n"
                "#!/bin/sh\n"
                "trap '' TERM\n"
                f"echo \"$PERSONA_OMP_LIVE\" > \"{work_record}\"\n"
                "sleep 30\n"
                "EOF\n"
                "chmod +x \"$out\"\n"
                "exit 0\n"
            )
            fake_go.chmod(0o755)

            env = os.environ.copy()
            env["PATH"] = str(tools)
            env["HERDR_SOCKET_PATH"] = "/tmp/caller-should-never-be-inherited.sock"
            env["PERSONA_OMP_WATCHDOG_TIMEOUT"] = "1"
            env["PERSONA_OMP_WATCHDOG_GRACE"] = "1"
            result = subprocess.run(
                [bash, str(SCRIPT)], cwd=str(ROOT), env=env,
                capture_output=True, text=True, timeout=15,
            )

            output = result.stdout + result.stderr
            self.assertNotEqual(0, result.returncode, output)
            self.assertIn("RESULT fail:", output)
            # Server startup must NOT see the caller's inherited HERDR_SOCKET_PATH.
            herdr_lines = herdr_log.read_text().splitlines()
            server_starts = [line for line in herdr_lines if " server " in f"{line} " and "server stop" not in line]
            self.assertTrue(server_starts, herdr_lines)
            self.assertIn("HERDR_SOCKET_PATH=<unset>", server_starts[0], server_starts)
            self.assertNotIn("/tmp/caller-should-never-be-inherited.sock", herdr_log.read_text())
            # Temp work directory must be cleaned up on exit.
            self.assertTrue(work_record.exists(), output)
            work_dir = Path(work_record.read_text().strip())
            self.assertFalse(work_dir.exists(), f"temp dir {work_dir} was not cleaned up")


if __name__ == "__main__":
    unittest.main()
