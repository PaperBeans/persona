import { describe, expect, it } from '@ohos/hypium';
import { util } from '@kit.ArkTS';
import { HubAccess } from '../../../main/ets/core/HubAccess';
import { Wire } from '../../../main/ets/models/Wire';
import { CredentialVault } from '../../../main/ets/platform/CredentialVault';
import { EndpointConfig, HttpFailure, HubTransport, hubCredentialOrigin } from '../../../main/ets/platform/HubTransport';
import { Pairing } from '../../../main/ets/platform/Pairing';
import { nativeTestContext } from './NativeContext';

export function nativeHubAccessTests(address: string, pin: string): void {
  describe('NativeHubAccess', (): void => {
    it('pairs_and_reconnects_http_and_websocket_from_one_private_origin_scoped_asset', 0, async (): Promise<void> => {
      const base = address.replace(/\/+$/, '') + '/edge/', token = 'synthetic-fixture-token';
      const missing = Pairing.manual(base, token, pin), wrong = Pairing.manual(base, token, pin);
      wrong.setCloudflareAccess('test-id.access', 'incorrect-example');
      for (const pair of [missing, wrong]) {
        let code = '';
        try { await pair.inspect(); } catch (e) { if (e instanceof HttpFailure) code = e.code; }
        expect(code).assertEqual(pair === missing ? 'cloudflare_access_required' : 'cloudflare_access_rejected');
      }

      const pair = Pairing.manual(base, token, pin);
      pair.setCloudflareAccess('test-id.access', 'synthetic-edge-secret');
      const target = await pair.inspect(), accepted = await pair.redeem(target, 'Synthetic native test');
      const vault = new CredentialVault(nativeTestContext()), id = 'access-' + util.generateRandomUUID();
      let direct: HubTransport | undefined, other: HubTransport | undefined;
      try {
        await vault.put(id, accepted.token, accepted.access);
        const restarted = new CredentialVault(nativeTestContext());
        let restored: HubAccess | undefined;
        const deviceToken = await restarted.get(id, (access: HubAccess | undefined): void => { restored = access; });
        expect(deviceToken).assertEqual(token);
        const conf = new EndpointConfig(base, target.hubId, deviceToken || '', pin, '', [], undefined, restored);
        direct = new HubTransport(conf);
        expect(Wire.parse(await direct.request('/api/v1/test/access-headers'))['sent']).assertTrue();
        expect(Wire.text(Wire.parse(await direct.request('/api/v1/test/client')), 'client').startsWith('harmony/')).assertTrue();
        await direct.connect((): void => {});
        expect(direct.hello(['elicitation'])).assertTrue();
        const stream = direct.stream('/api/v1/ws');
        try { await stream.connect((): void => {}); expect(stream.hello([])).assertTrue(); } finally { stream.close(); }
        const different = HubAccess.fromInput(hubCredentialOrigin('https://another.invalid:8443/'), 'test-id.access', 'synthetic-edge-secret');
        other = new HubTransport(new EndpointConfig(address, target.hubId, token, pin, '', [], undefined, different));
        expect(Wire.parse(await other.request('/api/v1/test/access-headers'))['sent']).assertFalse();
      } finally {
        direct?.close(); other?.close(); await vault.remove(id);
      }
      expect((await vault.get(id)) === null).assertTrue();
    });
  });
}
