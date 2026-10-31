"""Tests for the stale Postgres lock check that boot.sh runs.

After a power cut the data directory keeps a postmaster.pid naming a PID that,
after the reboot, belongs to something else. Postgres sees it alive, refuses to
start, and the whole stack waits on it (2026-10-31: PID 830 had become a Python
server). Each case writes a postmaster.pid naming a real process and asks the
script what it makes of it.
"""
import os
import shutil
import subprocess
import tempfile

_here = os.path.dirname(os.path.abspath(__file__))
SCRIPT = os.path.join(_here, "pg-stale-lock.sh")


def _datadir_with_lock(pid):
    d = tempfile.mkdtemp()
    with open(os.path.join(d, "postmaster.pid"), "w") as f:
        # The real file's shape: PID, data directory, start time, port, socket dir.
        f.write(f"{pid}\n{d}\n1793430667\n5432\n/tmp\n")
    return d


def _run(datadir):
    return subprocess.run(["bash", SCRIPT, datadir], capture_output=True, text=True)


def _spawn(name):
    """A live process whose command name is `name`."""
    d = tempfile.mkdtemp()
    exe = os.path.join(d, name)
    shutil.copy("/bin/sleep", exe)
    return subprocess.Popen([exe, "60"]), d


def test_a_lock_naming_some_other_live_process_is_moved_aside():
    other, bindir = _spawn("python3")
    try:
        d = _datadir_with_lock(other.pid)
        r = _run(d)
        assert r.returncode == 0, r.stderr
        assert not os.path.exists(os.path.join(d, "postmaster.pid"))
        assert [n for n in os.listdir(d) if n.startswith("postmaster.pid.stale-")]
        assert str(other.pid) in r.stderr
    finally:
        other.kill()
        shutil.rmtree(bindir)


def test_a_lock_naming_a_live_postgres_is_left_alone():
    pg, bindir = _spawn("postgres")
    try:
        d = _datadir_with_lock(pg.pid)
        assert _run(d).returncode == 0
        assert os.path.exists(os.path.join(d, "postmaster.pid"))
    finally:
        pg.kill()
        shutil.rmtree(bindir)


def test_a_lock_naming_a_dead_process_is_left_to_postgres():
    # Postgres clears that one itself, and may be rewriting the file right now.
    gone = subprocess.Popen(["/usr/bin/true"])
    gone.wait()
    d = _datadir_with_lock(gone.pid)
    assert _run(d).returncode == 0
    assert os.path.exists(os.path.join(d, "postmaster.pid"))


def test_no_lock_is_nothing_to_do():
    d = tempfile.mkdtemp()
    r = _run(d)
    assert r.returncode == 0
    assert os.listdir(d) == []
