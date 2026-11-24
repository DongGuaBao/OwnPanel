"""Run one UFW transaction over SSH or recover it from a native systemd service."""

import datetime
import fcntl
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time


ROOT = Path(os.environ.get("OWNPANEL_FIREWALL_ROOT", "/var/lib/ownpanel-ssh-firewall"))
STATE = ROOT / "pending.json"
RECOVERY = ROOT / "recover.py"
ACTIVE_PHASES = {"armed", "pending", "recovering"}


class Conflict(Exception):
    pass


def command(*args):
    completed = subprocess.run(
        args,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        timeout=20,
        env={**os.environ, "LC_ALL": "C"},
    )
    if completed.returncode:
        raise RuntimeError(completed.stdout[-12000:])
    return completed.stdout


def input_command(arguments, content):
    completed = subprocess.run(arguments, input=content, text=True, stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT, timeout=30, env={**os.environ, "LC_ALL":"C"})
    if completed.returncode:
        raise RuntimeError(completed.stdout[-12000:])
    return completed.stdout

def iptables_program(value):
    if value not in {"iptables","ip6tables","iptables-legacy","ip6tables-legacy","iptables-nft","ip6tables-nft"}:
        raise ValueError("Invalid iptables program")
    if not shutil.which(value+"-save") or not shutil.which(value+"-restore"):
        raise ValueError("Requested iptables backend is not installed")
    return value

def native_pending():
    lease=ROOT/"native-lease.json"
    if lease.exists():
        native=Path(json.loads(lease.read_text())["path"])
        if native.exists():
            saved=json.loads(native.read_text())
            identifier=saved.get("id","")
            if re.fullmatch(r"[a-f0-9]{16}",identifier):
                folder=native.parent/"firewall"/identifier
                if (folder/"restored").exists() or (folder/"confirmed").exists():return
            raise Conflict("An Agent UFW transaction is awaiting confirmation or recovery")

def pending_summary(state):
    return {key:state[key] for key in ("id","unit","expires")} if state and state["phase"] in ACTIVE_PHASES else None

def capture_rules(program):
    # Absent tables still have implicit ACCEPT policies. Include those policies so
    # rollback also clears a table first created by the pending change.
    raw=command(program+"-save","-c")
    present=set(re.findall(r"^\*(\w+)$",raw,re.M))
    for table in ("filter","nat","mangle","raw","security"):
        if table in present:continue
        try: policies=command(program,"-w","5","-t",table,"-S")
        except RuntimeError:continue
        chains=[];rules=[]
        for line in policies.splitlines():
            parts=line.split()
            if parts and parts[0]=="-P":chains.append(":"+parts[1]+" "+parts[2]+" [0:0]")
            elif parts and parts[0]=="-N":chains.append(":"+parts[1]+" - [0:0]")
            elif line:rules.append(line)
        if chains:raw+="\n*"+table+"\n"+"\n".join(chains+rules)+"\nCOMMIT\n"
    return raw

def iptables_status(request,state):
    program=iptables_program(request.get("program","iptables"))
    tables=[]
    for table in ("filter","nat","mangle","raw","security"):
        try: tables.append({"name":table,"text":command(program,"-w","5","-t",table,"-L","-n","-v","--line-numbers","-x")})
        except RuntimeError as error: tables.append({"name":table,"error":str(error)})
    return {"program":program,"version":command(program,"--version"),
        "programs":[p for p in ("iptables","ip6tables","iptables-legacy","ip6tables-legacy","iptables-nft","ip6tables-nft") if shutil.which(p+"-save") and shutil.which(p+"-restore")],
        "rules":capture_rules(program),"tables":tables,"pending":pending_summary(state)}

def write_atomic(destination, content, mode):
    descriptor, temporary = tempfile.mkstemp(dir=destination.parent, prefix=".ownpanel-")
    try:
        with os.fdopen(descriptor, "w") as stream:
            stream.write(content)
            os.fchmod(stream.fileno(), mode)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, destination)
        directory = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        Path(temporary).unlink(missing_ok=True)


def save_state(state):
    write_atomic(STATE, json.dumps(state), 0o600)


def read_state():
    if not STATE.exists():
        return None
    state = json.loads(STATE.read_text())
    identifier = state.get("id", "")
    if not re.fullmatch(r"[a-f0-9]{16}", identifier):
        raise RuntimeError("Invalid saved firewall transaction")
    if state.get("unit") != "ownpanel-ssh-fw-" + identifier:
        raise RuntimeError("Invalid saved firewall recovery unit")
    if state.get("phase") not in ACTIVE_PHASES | {"confirmed", "restored"}:
        raise RuntimeError("Invalid saved firewall transaction state")
    return state


def stop_timer(state):
    if (Path("/etc/systemd/system") / (state["unit"] + ".timer")).exists():
        command("systemctl", "disable", "--now", state["unit"] + ".timer")


def restore(state):
    if state["phase"] not in ACTIVE_PHASES:
        return
    state["phase"] = "recovering"
    save_state(state)
    if state.get("backend")=="iptables":
        program=iptables_program(state["program"])
        input_command([program+"-restore","--wait","5","--counters"],(ROOT/state["id"]/"rules.txt").read_text())
    else:
        command("tar", "-xf", str(ROOT / state["id"] / "rules.tar"), "-C", "/")
        if state["active"]:
            command("ufw", "--force", "enable")
            command("ufw", "reload")
        else:
            command("ufw", "disable")
        for program in state.get("rawPrograms",[]):
            program=iptables_program(program)
            input_command([program+"-restore","--wait","5","--counters"],(ROOT/state["id"]/(program+".rules")).read_text())
    state["phase"] = "restored"
    save_state(state)
    stop_timer(state)


def install_recovery(state, source):
    # The recovery unit runs without the Agent's environment, including after reboot.
    source = "import os\nos.environ['OWNPANEL_FIREWALL_ROOT'] = " + repr(str(ROOT)) + "\n" + source
    write_atomic(RECOVERY, source, 0o600)
    service = Path("/etc/systemd/system") / (state["unit"] + ".service")
    timer = service.with_suffix(".timer")
    deadline = datetime.datetime.fromtimestamp(
        state["deadline"], datetime.timezone.utc
    ).strftime("%Y-%m-%d %H:%M:%S UTC")
    write_atomic(service,
        "[Unit]\nDescription=OwnPanel SSH firewall recovery\n"
        "After=local-fs.target\n"
        "[Service]\nType=oneshot\n"
        f"ExecStart=/usr/bin/python3 {RECOVERY} recover {state['id']}\n"
        "Restart=on-failure\nRestartSec=10\nTimeoutStartSec=90\n", 0o644
    )
    write_atomic(timer,
        "[Unit]\nDescription=OwnPanel SSH firewall recovery deadline\n"
        "[Timer]\n"
        f"OnCalendar={deadline}\nOnActiveSec=60s\nUnit={state['unit']}.service\n"
        "Persistent=true\nAccuracySec=1s\n"
        "[Install]\nWantedBy=timers.target\n", 0o644
    )
    command("systemctl", "daemon-reload")
    command("systemctl", "enable", "--now", state["unit"] + ".timer")
    command("systemctl", "is-active", "--quiet", state["unit"] + ".timer")


def cleanup_previous(state):
    if not state:
        return
    stop_timer(state)
    for suffix in (".timer", ".service"):
        (Path("/etc/systemd/system") / (state["unit"] + suffix)).unlink(
            missing_ok=True
        )
    command("systemctl", "daemon-reload")


def status(state):
    pending = None
    if state and state["phase"] in ACTIVE_PHASES:
        pending = {key: state[key] for key in ("id", "unit", "expires")}
        if time.time() >= state["deadline"]:
            command("systemctl", "start", "--no-block", state["unit"] + ".service")
    return {
        "text": command("ufw", "status", "numbered"),
        "added": command("ufw", "show", "added"),
        "pending": pending,
        "backend": "ufw",
        "readOnly": False,
    }


def apply(request, previous):
    if previous and previous["phase"] in ACTIVE_PHASES:
        raise Conflict("Please confirm the pending change or wait for recovery")
    cleanup_previous(previous)
    identifier = request["id"]
    if not re.fullmatch(r"[a-f0-9]{16}", identifier):
        raise ValueError("Invalid transaction ID")
    transaction = ROOT / identifier
    transaction.mkdir(mode=0o700)
    backend=request.get("backend","ufw")
    if backend=="ufw" and request.get("original"):
        if "ufw "+request["original"].strip() not in command("ufw","show","added").splitlines():
            raise Conflict("UFW rule changed; reload before editing")
    native_pending()
    if backend=="iptables":
        program=iptables_program(request["program"])
        input_command([program+"-restore","--test","--wait","5","--counters"],request["rules"])
        write_atomic(transaction/"rules.txt",capture_rules(program),0o600)
    elif backend=="ufw":
        command("tar","-cf",str(transaction/"rules.tar"),"-C","/","etc/ufw","etc/default/ufw")
        with (transaction/"rules.tar").open("rb") as archive: os.fsync(archive.fileno())
        for raw in ("iptables","ip6tables"):
            if shutil.which(raw+"-save") and shutil.which(raw+"-restore"):
                write_atomic(transaction/(raw+".rules"),capture_rules(raw),0o600)
    else: raise ValueError("Invalid firewall backend")
    deadline = int(time.time()) + 60
    state = {
        "id": identifier,
        "unit": "ownpanel-ssh-fw-" + identifier,
        "deadline": deadline,
        "expires": datetime.datetime.fromtimestamp(
            deadline, datetime.timezone.utc
        ).isoformat(),
        "phase": "armed",
        "active": backend=="ufw" and "Status: active" in command("ufw", "status"),
        "backend":backend, "program":request.get("program",""),
        "rawPrograms":[p for p in ("iptables","ip6tables") if (transaction/(p+".rules")).exists()],
    }
    save_state(state)
    try:
        install_recovery(state, request["recoveryProgram"])
        if backend=="ufw" and request["operation"] == "enable":
            port = int(request["sshPort"])
            if not 1 <= port <= 65535:
                raise ValueError("Invalid SSH server port")
            command("ufw", "prepend", "allow", str(port) + "/tcp")
        if backend=="iptables":
            output=input_command([program+"-restore","--wait","5","--counters"],request["rules"])
        else:
            output=""
            for arguments in request.get("commands",[request["arguments"]]):
                output+=command("ufw",*arguments)
        state["phase"] = "pending"
        save_state(state)
    except Exception:
        restore(state)
        raise
    return {"output": output, "pending": pending_summary(state)}


def execute(request):
    state = read_state()
    action = request["action"]
    if action == "iptables-status":
        return iptables_status(request,state)
    if action == "status":
        return status(state)
    if action == "apply":
        return apply(request, state)
    if action == "confirm":
        if (
            not state
            or state["id"] != request["id"]
            or state["phase"] != "pending"
            or state["deadline"] - time.time() < 3
        ):
            raise Conflict("Firewall change expired or recovery is already in progress")
        # The recovery service takes the same lock and observes this durable decision.
        state["phase"] = "confirmed"
        save_state(state)
        stop_timer(state)
        return {"ok": True}
    raise ValueError("Unsupported firewall operation")


def main():
    if os.geteuid() != 0:
        raise PermissionError("Firewall management requires root")
    os.umask(0o077)
    ROOT.mkdir(mode=0o700, exist_ok=True)
    with (ROOT / "lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if len(sys.argv) == 3 and sys.argv[1] == "recover":
            state = read_state()
            if state and state["id"] == sys.argv[2]:
                restore(state)
            return
        try:
            result = execute(REQUEST)
        except Conflict as error:
            result = {"error": str(error), "status": 409}
        print(json.dumps(result))


if __name__ == "__main__":
    main()
