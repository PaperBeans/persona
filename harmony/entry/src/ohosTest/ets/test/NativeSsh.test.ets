import { describe, it, expect } from '@ohos/hypium';
import { fileIo } from '@kit.CoreFileKit';
import { util, url } from '@kit.ArkTS';
import nativeSsh from 'libpersona_ssh.so';
import type { NativeSshConfig, NativeSshLease } from 'libpersona_ssh.so';
import { Pairing } from '../../../main/ets/platform/Pairing';
import { SshDeviceIdentity } from '../../../main/ets/platform/SshDeviceIdentity';
import { PairingSsh } from '../../../main/ets/models/PairingSsh';
import { SshConfiguration } from '../../../main/ets/models/SshTunnel';
import { ClientModel } from '../../../main/ets/models/ClientModel';
import { SshCredentialVault } from '../../../main/ets/platform/SshCredentialVault';
import { HubTransport, EndpointConfig, TransportProxy } from '../../../main/ets/platform/HubTransport';
import { Wire } from '../../../main/ets/models/Wire';
import { fixtureArgument } from './NativeTransport.test';
import { nativeTestContext } from './NativeContext';
import { connection, http } from '@kit.NetworkKit';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { asset } from '@kit.AssetStoreKit';
import { BusinessError } from '@kit.BasicServicesKit';

async function expectErased(profile: string): Promise<void> {
  const query: asset.AssetMap = new Map();
  query.set(asset.Tag.DATA_LABEL_CRITICAL_1, new util.TextEncoder().encodeInto('persona.harmony.ssh.v1'));
  query.set(asset.Tag.DATA_LABEL_CRITICAL_2, new util.TextEncoder().encodeInto(profile));
  query.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ATTRIBUTES);
  let count = 0;
  try { count = (await asset.query(query)).length; }
  catch (error) { if ((error as BusinessError).code !== 24000002) throw error as Error; }
  expect(count).assertEqual(0);
}

let cachedFixture: Record<string, Object> | undefined;

async function sshFixture(): Promise<Record<string, Object>> {
  if (cachedFixture) return cachedFixture;
  const name = fixtureArgument('sshFixtureFile');
  if (!/^persona-ssh-[a-z0-9-]+\.json$/.test(name)) throw new Error('invalid_fixture');
  const portText = fixtureArgument('sshFixturePort');
  if (!portText) return Wire.parse(fileIo.readTextSync(nativeTestContext().filesDir + '/' + name));
  // Release-configured test bundles have no HDC debug sandbox. Receive the ephemeral test key in this
  // (separately installed) feature through its own local test listener, bound to the supplied digest.
  const port = Number(portText), ticket = fixtureArgument('sshFixtureTicket'), expected = fixtureArgument('sshFixtureSHA256');
  if (!/^\d{1,5}$/.test(portText) || port < 1 || port > 65535 || !/^[a-f0-9]{64}$/.test(ticket) || !/^[a-f0-9]{64}$/.test(expected)) throw new Error('invalid_fixture');
  const input = http.createHttp();
  try {
    const answer = await input.request('http://127.0.0.1:' + port + '/fixture', {
      method: http.RequestMethod.GET, header: { 'Authorization': 'Bearer ' + ticket }, connectTimeout: 5000, readTimeout: 5000,
      usingCache: false
    });
    if (answer.responseCode !== 200 || typeof answer.result !== 'string' || answer.result.length > 48000) throw new Error('invalid_fixture');
    const hash = cryptoFramework.createMd('SHA256'); hash.updateSync({data:new util.TextEncoder().encodeInto(answer.result)});
    const actual = Array.from(hash.digestSync().data).map((byte: number): string => byte.toString(16).padStart(2,'0')).join('');
    if (actual !== expected) throw new Error('invalid_fixture');
    cachedFixture = Wire.parse(answer.result); return cachedFixture;
  } finally { input.destroy(); }
}

async function configuration(wrongPin: boolean = false): Promise<NativeSshConfig> {
  const fixture = await sshFixture();
  const targetHost = Wire.text(fixture, 'targetHost');
  const literal = targetHost.includes(':') || /^\d{1,3}(\.\d{1,3}){3}$/.test(targetHost);
  const resolvedHubIps: string[] = literal ? [] : (await connection.getAddressesByName(targetHost)).slice(0, 16).map((value: connection.NetAddress): string => value.address);
  return { id: util.generateRandomUUID(), sshHost: '127.0.0.1', sshPort: Wire.number(fixture, 'sshPort'),
    username: Wire.text(fixture, 'username'), hostKey: wrongPin ? 'SHA256:' + 'A'.repeat(43) : Wire.text(fixture, 'hostKey'),
    targetHost: targetHost, targetPort: Wire.number(fixture, 'targetPort'), resolvedHubIps: resolvedHubIps,
    privateKey: new util.TextEncoder().encodeInto(Wire.text(fixture, 'privateKey')), passphrase: new Uint8Array(0) };
}
class FixtureProxy implements TransportProxy {
  readonly host: string; readonly port: number; readonly username: string; readonly password: string;
  private readonly id: string;
  constructor(lease: NativeSshLease) { this.host = lease.host; this.port = lease.port; this.username = lease.username; this.password = lease.password; this.id = lease.id; }
  isCurrent(): boolean { return nativeSsh.isOpen(this.id); }
}
function transport(proxy: TransportProxy, badPin: boolean = false): HubTransport {
  return new HubTransport(new EndpointConfig(fixtureArgument('fixtureAddress'), '00000000-0000-4000-8000-000000000001',
    'synthetic-fixture-token', badPin ? 'A'.repeat(43) : fixtureArgument('fixturePin'), 'synthetic', [], proxy));
}
export default function nativeSshTests(): void {
  describe('NativeSsh', (): void => {
    it('device_only_asset_chunks_round_trip_and_forget_without_cross_profile_access', 0, async (): Promise<void> => {
      const vault = new SshCredentialVault(), profile = 'ssh-fixture-' + util.generateRandomUUID(), other = profile + '-other';
      const key = 'synthetic-key\n'.repeat(240), password = 'synthetic-passphrase'.repeat(110);
      vault.allow(profile); vault.allow(other);
      try {
        const reference = await vault.put(profile, key, password, (): boolean => true);
        const unrelated = await vault.put(other, key, '', (): boolean => true);
        const value = await vault.get(profile, reference, (): boolean => true);
        expect(value.privateKey.length).assertEqual(new util.TextEncoder().encodeInto(key).length);
        expect(value.passphrase.length).assertEqual(new util.TextEncoder().encodeInto(password).length);
        expect(util.TextDecoder.create('utf-8').decodeToString(value.privateKey)).assertEqual(key);
        expect(util.TextDecoder.create('utf-8').decodeToString(value.passphrase)).assertEqual(password); value.clear();
        await vault.forget(profile);
        await expectErased(profile);
        let denied = false; try { await vault.get(profile, reference, (): boolean => true); } catch (_) { denied = true; } expect(denied).assertTrue();
        const retained = await vault.get(other, unrelated, (): boolean => true); expect(retained.privateKey.length > 1024).assertTrue(); retained.clear();
        await vault.forget(other);
        await expectErased(other);
      } finally { await vault.forget(profile).catch((): void => {}); await vault.forget(other).catch((): void => {}); }
    });
    it('native_ssh_rejects_an_untrusted_host_key', 0, async (): Promise<void> => {
      const config = await configuration(true); let rejected = false;
      try { await nativeSsh.open(config); } catch (_) { rejected = true; }
      finally { config.privateKey.fill(0); await nativeSsh.close(config.id); }
      expect(rejected).assertTrue(); expect(nativeSsh.isOpen(config.id)).assertFalse();
    });
    it('first_pairing_uses_the_confirmed_ssh_route_and_clears_cancelled_credentials', 0, async (): Promise<void> => {
      const fixture = await configuration(), targetAddress = fixtureArgument('fixtureAddress'), pin = fixtureArgument('fixturePin');
      const setup = new PairingSsh(), configurationDraft = new SshConfiguration();
      configurationDraft.sshHost = fixture.sshHost; configurationDraft.sshPort = fixture.sshPort;
      configurationDraft.username = fixture.username; configurationDraft.hostKey = fixture.hostKey;
      const key = util.TextDecoder.create('utf-8').decodeToString(fixture.privateKey); fixture.privateKey.fill(0);
      const client = new ClientModel(); await client.initialize(nativeTestContext());
      const rawPair = (): Pairing => Pairing.manual(targetAddress, 'synthetic-fixture-token');
      const active = (): boolean => true;
      let paired = '', setupRejected = false, cancelRejected = false, wrongHostRejected = false, wrongTlsRejected = false;
      try {
        try { setup.configure(configurationDraft, key, '', pin, false); } catch (_) { setupRejected = true; }
        expect(setupRejected).assertTrue(); expect(setup.configured).assertFalse();
        const wrong = configurationDraft.copy(); wrong.hostKey = 'SHA256:' + 'A'.repeat(43);
        setup.configure(wrong, key, '', pin, true);
        try { await setup.prepare(rawPair(), active).inspect(); } catch (_) { wrongHostRejected = true; }
        expect(wrongHostRejected).assertTrue();
        setup.configure(configurationDraft, key, '', 'A'.repeat(43), true);
        try { await setup.prepare(rawPair(), active).inspect(); } catch (_) { wrongTlsRejected = true; }
        expect(wrongTlsRejected).assertTrue();
        setup.configure(configurationDraft, key, '', pin, true);
        const cancelled = setup.prepare(rawPair(), active), first = await cancelled.inspect();
        setup.cancelAttempt();
        try { await cancelled.redeem(first); } catch (_) { cancelRejected = true; }
        expect(cancelRejected).assertTrue(); await expectErased(first.hubId);
        const pair = setup.prepare(rawPair(), active), target = await pair.inspect(); paired = target.hubId;
        const proxy = await pair.sshRoute!.open(target.baseUrl, target.pin);
        const witness = new HubTransport(new EndpointConfig(target.baseUrl, paired, 'synthetic-fixture-token', pin, '', [], proxy));
        let before = 0;
        try { before = Wire.number(Wire.parse(await witness.request('/api/v1/test/stats')), 'token_issues'); }
        finally { witness.close(); }
        await client.pair(pair, target, active);
        expect(proxy.isCurrent()).assertFalse(); expect(client.profile.id).assertEqual(paired);
        const rebound = await client.ssh.acquire(); expect(!!rebound && rebound.isCurrent()).assertTrue();
        const record = SshConfiguration.from(await client.loadView('ssh-tunnel'));
        expect(record.enabled).assertTrue(); expect(record.hostKey).assertEqual(configurationDraft.hostKey); expect(!!record.keyRef).assertTrue();
        const after = new HubTransport(new EndpointConfig(target.baseUrl, paired, 'synthetic-fixture-token', pin, '', [], rebound));
        try { expect(Wire.number(Wire.parse(await after.request('/api/v1/test/stats')), 'token_issues')).assertEqual(before + 1); }
        finally { after.close(); }
      } finally {
        setup.clear();
        if (paired && client.profiles.some(profile => profile.id === paired)) await client.forgetProfile(paired);
        if (paired) await expectErased(paired);
        await client.background();
      }
    });
    if (fixtureArgument('sshGeneratedKeyProbe') === '1') it('native_device_generated_ed25519_authorizes_only_its_hub_and_survives_forget', 0, async (): Promise<void> => {
      const fixture = await sshFixture(), base = fixtureArgument('fixtureAddress'), pin = fixtureArgument('fixturePin');
      const key = await SshDeviceIdentity.publicIdentity('persona-native-test');
      expect(key.line.startsWith('ssh-ed25519 ')).assertTrue(); expect(key.fingerprint.startsWith('SHA256:')).assertTrue();
      const endpoint = url.URL.parseURL(base);
      const restricted = key.restrictedLine(endpoint.hostname, Number(endpoint.port));
      const ctrl = http.createHttp();
      try {
        const port = Number(fixtureArgument('sshFixturePort')), token = fixtureArgument('sshFixtureTicket');
        if (!Number.isInteger(port) || port < 1 || port > 65535 || !/^[a-f0-9]{64}$/.test(token)) throw new Error('invalid_fixture');
        const response = await ctrl.request('http://127.0.0.1:' + port + '/authorize-generated', {
          method: http.RequestMethod.POST, header: {'Authorization':'Bearer ' + token, 'Content-Type':'text/plain; charset=utf-8'},
          extraData:restricted, connectTimeout:5000, readTimeout:5000, usingCache:false
        });
        expect(response.responseCode).assertEqual(204);
      } finally { ctrl.destroy(); }
      const config = new SshConfiguration(); config.sshHost = '127.0.0.1'; config.sshPort = Wire.number(fixture, 'sshPort');
      config.username = Wire.text(fixture,'username'); config.hostKey = Wire.text(fixture,'hostKey');
      const client = new ClientModel(), draft = new PairingSsh(); let hub = '';
      await client.initialize(nativeTestContext());
      try {
        const observed = await draft.observeServer(config.sshHost, config.sshPort);
        expect(observed.algorithm).assertEqual('ssh-ed25519'); expect(observed.fingerprint).assertEqual(config.hostKey);
        const wrong = config.copy(); wrong.hostKey = 'SHA256:' + 'A'.repeat(43);
        let mismatch = '';
        try { await draft.testForward(wrong, '', '', true, endpoint.hostname, Number(endpoint.port)); }
        catch (error) { mismatch = (error as Error).message; }
        expect(mismatch).assertEqual('ssh_host_key_mismatch');
        await draft.testForward(config, '', '', true, endpoint.hostname, Number(endpoint.port));
        draft.configure(config, '', '', pin, true, true);
        const pair = draft.prepare(Pairing.manual(base,'synthetic-fixture-token'), (): boolean => true), target = await pair.inspect();
        hub = target.hubId; await client.pair(pair,target); const stored = SshConfiguration.from(await client.loadView('ssh-tunnel'));
        expect(stored.deviceIdentity).assertTrue(); expect(stored.keyRef).assertEqual('');
        const lease = await client.ssh.acquire(); expect(!!lease && lease.isCurrent()).assertTrue();
        await expectErased(hub);
      } finally {
        draft.clear();
        if (hub && client.profiles.some(profile => profile.id === hub)) await client.forgetProfile(hub);
        await client.background();
      }
      const again = await SshDeviceIdentity.publicIdentity('persona-native-test');
      expect(again.fingerprint).assertEqual(key.fingerprint); expect(again.line).assertEqual(key.line);
    });
    if (fixtureArgument('sshPolicyProbe') === '1') it('separate_forward_target_keeps_hub_identity_for_direct_fallback_and_always', 0, async (): Promise<void> => {
      const fixture = await sshFixture(), pin = fixtureArgument('fixturePin'), port = Wire.number(fixture,'targetPort');
      const reachable = fixtureArgument('sshDirectAddress'), unreachable = fixtureArgument('sshOfflineAddress');
      const server = Wire.text(fixture,'hostKey'), key = Wire.text(fixture,'privateKey');
      const gated = fixtureArgument('sshDirectGate') === '1';
      const setDirect = async (available: boolean): Promise<void> => {
        const probe = http.createHttp();
        try {
          const control = Number(fixtureArgument('sshFixturePort')), ticket = fixtureArgument('sshFixtureTicket');
          if (!Number.isInteger(control) || control < 1 || control > 65535 || !/^[a-f0-9]{64}$/.test(ticket)) throw new Error('invalid_fixture');
          const reply = await probe.request('http://127.0.0.1:' + control + '/direct-availability', {
            method:http.RequestMethod.POST, header:{'Authorization':'Bearer ' + ticket,'Content-Type':'text/plain'},
            extraData:available ? 'up' : 'down',connectTimeout:5000,readTimeout:5000,usingCache:false });
          expect(reply.responseCode).assertEqual(204);
        } finally { probe.destroy(); }
      };
      for (const scenario of ['direct','fallback','always']) {
        const config = new SshConfiguration(); config.sshHost = '127.0.0.1'; config.sshPort = Wire.number(fixture,'sshPort');
        config.username = Wire.text(fixture,'username'); config.hostKey = server;
        config.routePolicy = scenario === 'always' ? 'always' : 'fallback'; config.forwardHost = '127.0.0.1'; config.forwardPort = port;
        const base = !gated && scenario === 'fallback' ? unreachable : reachable;
        expect(Number(url.URL.parseURL(base).port) !== port).assertTrue();
        const client = new ClientModel(), setup = new PairingSsh(); let hub = '';
        await client.initialize(nativeTestContext());
        try {
          if (gated) await setDirect(scenario !== 'fallback');
          setup.configure(config,key,'',pin,true);
          const pair = setup.prepare(Pairing.manual(base,'synthetic-fixture-token'),(): boolean => true), target = await pair.inspect();
          hub = target.hubId; expect(target.baseUrl).assertEqual(base + '/');
          expect(pair.sshRoute!.directCurrent?.(target.baseUrl,pin) === true).assertEqual(scenario === 'direct');
          await client.pair(pair,target);
          const kept = SshConfiguration.from(await client.loadView('ssh-tunnel'));
          expect(kept.routePolicy).assertEqual(config.routePolicy); expect(kept.forwardPort).assertEqual(port);
          expect(kept.forwardHost).assertEqual('127.0.0.1'); expect(kept.hostKey).assertEqual(server);
          expect(client.ssh.phase).assertEqual(scenario === 'direct' ? 'direct' : 'connected');
          if (gated && scenario === 'fallback' && fixtureArgument('sshRecheck') === '1') {
            const generation = client.connectionGeneration(); await setDirect(true);
            for (let second=0; second<138; second++) {
              if (client.connectionGeneration() > generation && client.ssh.phase === 'direct') break;
              await new Promise<void>((resolve: () => void) => {setTimeout(resolve,1000);});
            }
            expect(client.connectionGeneration() > generation && client.ssh.phase === 'direct').assertTrue();
            expect(client.profile.hubId).assertEqual(hub);
          }
          if (gated && scenario === 'always') {
            const broken = kept.copy(); broken.sshPort = Number(url.URL.parseURL(unreachable).port);
            await client.ssh.save(broken,'','',true);
            await client.selectProfile(client.profile,true);
            expect(client.ssh.phase).assertEqual('direct'); expect(client.profile.hubId).assertEqual(hub);
          }
        } finally {
          if (gated) await setDirect(true);
          setup.clear(); if (hub && client.profiles.some(profile => profile.id === hub)) await client.forgetProfile(hub);
          if (hub) await expectErased(hub);
          await client.background();
        }
      }
    });
    it('native_authenticated_proxies_preserve_https_pinning_websocket_and_child_streams', 0, async (): Promise<void> => {
      const config = await configuration();
      let main: HubTransport | undefined, child: HubTransport | undefined, bad: HubTransport | undefined;
      try {
        const pending = nativeSsh.open(config); config.privateKey.fill(0); const lease = await pending;
        expect(lease.host).assertEqual('127.0.0.1'); const proxy = new FixtureProxy(lease); main = transport(proxy);
        const before = Wire.parse(await main.request('/api/v1/test/stats'));
        bad = transport(proxy, true); let rejected = false;
        try { await bad.request('/api/v1/test/forbidden'); } catch (_) { rejected = true; } expect(rejected).assertTrue();
        rejected = false; try { await bad.connect((): void => {}); } catch (_) { rejected = true; } expect(rejected).assertTrue(); bad.close(); bad = undefined;
        const after = Wire.parse(await main.request('/api/v1/test/stats'));
        expect(Wire.number(after, 'forbidden')).assertEqual(Wire.number(before, 'forbidden'));
        expect(Wire.number(after, 'ws')).assertEqual(Wire.number(before, 'ws'));
        await main.connect((): void => {}); expect(main.hello(['elicitation'])).assertTrue();
        child = main.stream('/api/v1/ws'); await child.connect((): void => {});
        const stats = Wire.parse(await main.request('/api/v1/test/stats')); expect(Wire.number(stats, 'ws') >= Wire.number(before, 'ws') + 2).assertTrue();
        const identity = Wire.parse(await main.request('/api/v1/test/identity')), logical = url.URL.parseURL(fixtureArgument('fixtureAddress'));
        const prefix = logical.pathname.replace(/\/+$/, ''), http = Wire.object(identity, 'http');
        expect(Wire.text(http, 'host')).assertEqual(logical.host); expect(Wire.text(http, 'path')).assertEqual(prefix + '/api/v1/test/identity');
        const hostname = logical.hostname.replace(/^\[|\]$/g, '');
        const domain = !hostname.includes(':') && !/^\d{1,3}(\.\d{1,3}){3}$/.test(hostname);
        if (domain) expect(Wire.text(http, 'sni')).assertEqual(hostname);
        const identities = Wire.rows(identity, 'websocket').slice(-2); expect(identities.length).assertEqual(2);
        for (const entry of identities) {
          expect(Wire.text(entry, 'host')).assertEqual(logical.host); expect(Wire.text(entry, 'path')).assertEqual(prefix + '/api/v1/ws');
          if (domain) expect(Wire.text(entry, 'sni')).assertEqual(hostname);
        }
        await nativeSsh.close(config.id); expect(proxy.isCurrent()).assertFalse();
        rejected = false; try { await main.request('/api/v1/test/forbidden'); } catch (_) { rejected = true; } expect(rejected).assertTrue();
        expect(child.send('{"type":"no_dispatch_after_close"}')).assertFalse();
      } finally { config.privateKey.fill(0); child?.close(); bad?.close(); main?.close(); await nativeSsh.close(config.id); }
    });
  });
}
