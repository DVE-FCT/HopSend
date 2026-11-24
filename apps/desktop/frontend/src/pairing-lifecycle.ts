import type { Device, DeviceObservation } from './bridge';

export interface PairCompletion {
  device: Device;
  connected: boolean;
  message: string;
}

// Saving trust and probing a live connection have different failure semantics.
// A probe failure must never make the caller repeat an already saved import.
export async function verifySavedPair(
  device: Device,
  api: {
    PrepareDevice(id: string): Promise<{ready: boolean}>;
    CheckDevice(id: string): Promise<Record<string, string>>;
  },
): Promise<PairCompletion> {
  try {
    if (device.manage_agent) {
      const result = await api.PrepareDevice(device.id);
      if (!result.ready) throw new Error('远端准备尚未通过');
    } else {
      await api.CheckDevice(device.id);
    }
    return {device, connected: true, message: device.manage_agent
      ? '配对资料已保存；设备准备、HTTPS 认证与 SFTP 字节验收通过。'
      : '配对资料已保存；HTTPS 与 SSH/SFTP 认证通过，尚未验证文件字节。'};
  } catch (error) {
    return {device, connected: false, message: `配对资料已保存，但连接未通过：${String(error)}`};
  }
}

export interface HealthState {
  loading: boolean;
  result?: DeviceObservation;
  error?: string;
}

// One bounded pool for initial, periodic and user-requested read-only probes.
// Failed bridge calls are also throttled; a timer cannot flood errors/retries.
export function createHealthMonitor(
  ids: () => string[],
  observe: (id: string) => Promise<DeviceObservation>,
  update: (id: string, state: HealthState) => void,
  now = () => Date.now(),
) {
  const attempted = new Map<string, number>();
  let running = false, stopped = false;
  let generation = 0;
  return {
    async refresh(force = false) {
      if (running || stopped) return;
      running = true;
      const startedGeneration = generation;
      try {
        const current = ids();
        for (const id of attempted.keys()) if (!current.includes(id)) attempted.delete(id);
        const queue = current.filter(id => force || !attempted.has(id) || now() - attempted.get(id)! >= 30_000);
        await Promise.all([0, 1].map(async () => {
          for (let id = queue.shift(); id && !stopped && generation === startedGeneration; id = queue.shift()) {
            attempted.set(id, now());
            update(id, {loading: true});
            try {
              const result = await observe(id);
              if (!stopped && generation === startedGeneration && ids().includes(id)) update(id, {loading: false, result});
            } catch (error) {
              if (!stopped && generation === startedGeneration && ids().includes(id)) update(id, {loading: false, error: String(error)});
            }
          }
        }));
      } finally {
        running = false;
      }
    },
    invalidate() {generation++;attempted.clear();},
    stop() {stopped = true;},
  };
}
