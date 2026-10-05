"""Provision dedicated native service secrets through SSH stdin, without printing them."""
import json
import os
import pathlib
import subprocess

stable = pathlib.Path('/usr/local/bin/op')
op = os.environ.get('FRICK_OP', str(stable) if stable.exists() else 'op')
def run(args, value=None):
    result = subprocess.run(args, input=value, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(args[0]+' failed; secret-bearing output withheld.')
    return result.stdout
def item(title):
    return json.loads(run([op,'item','get',title,'--vault','Personal','--format','json']))
def password(value):
    return next(f['value'] for f in value['fields'] if f['id']=='password').strip()
def transfer(host, name, value):
    run(['ssh',host,'umask 077; mkdir -p /home/mbrock/.config/frick-relay; chmod 700 /home/mbrock/.config/frick-relay; cat > /home/mbrock/.config/frick-relay/'+name+'; chmod 600 /home/mbrock/.config/frick-relay/'+name],value)
bank=item('Bank Frick Worker API')
service=item('Frick Worker Service')
relay=item('Frick Tailnet Relay')
private_key=next(f['value'] for f in bank['fields'] if f.get('label')=='worker_rsa_private_key')
transfer('Chapel','private-key.pem',private_key)
transfer('Chapel','.env','FRICK_API_KEY='+json.dumps(password(bank))+'\nFRICK_KEY_FILE="/home/mbrock/.config/frick-relay/private-key.pem"\nFRICK_RELAY_TOKEN='+json.dumps(password(relay))+'\n')
transfer('Igloo','frontend.env','SERVICE_TOKEN='+json.dumps(password(service))+'\nRELAY_TOKEN='+json.dumps(password(relay))+'\nRELAY_URL=http://frick.whale-justice.ts.net:8087\nMODE=live\n')
print('Dedicated native credentials provisioned. Restart the two services to load updates.')
