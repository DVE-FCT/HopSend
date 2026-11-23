export interface Device {
  id: string;
  name: string;
  agent_url: string;
  ssh_address: string;
  ssh_user: string;
  ca_file: string;
  token_file: string;
  key_file: string;
  known_hosts_file: string;
  share_id: string;
  remote_agent_config?: string;
  manage_agent: boolean;
}
export interface PreparationReport {
  ready: boolean;
  managed: boolean;
  observed_at: string;
  steps: { name: string; state: string; detail: string }[];
}
export interface Task {
  direction: string;
  id: string;
  task_id: string;
  name: string;
  destination: string;
  manifest_id: string;
  state: string;
  verified_bytes: number;
  total_bytes: number;
  completed_chunks: number;
  updated_at: string;
  error?: string;
  transferred_bytes: number;
  speed_bytes_per_second: number;
  eta_seconds: number;
  metrics_available: boolean;
}
export interface NetworkReport {
  observed_at: string;
  path: "direct" | "relay" | "unknown";
  transfer_verified: boolean;
  layers: {
    name: string;
    state: string;
    detail: string;
    milliseconds: number;
  }[];
}
export type TaskEvent = Pick<
  Task,
  "task_id" | "state" | "error" | "updated_at"
>;
interface API {
  PrepareDevice(id: string): Promise<PreparationReport>;
  DiagnoseDevice(id: string, manifest: string): Promise<NetworkReport>;
  StopDiagnostic(): Promise<void>;
  Activity(): Promise<TaskEvent[]>;
  PickFolder(): Promise<string>;
  StartUpload(
    device: string,
    share: string,
    source: string,
    target: string,
  ): Promise<string>;
  Devices(): Promise<Device[]>;
  SaveDevice(d: Device): Promise<Device>;
  ImportDevice(): Promise<Device>;
  CheckDevice(id: string): Promise<Record<string, string>>;
  PickFile(title: string): Promise<string>;
  PickDestination(name: string): Promise<string>;
  StartDownload(
    device: string,
    manifest: string,
    destination: string,
  ): Promise<string>;
  Tasks(): Promise<Task[]>;
  PauseTask(id: string): Promise<void>;
  ResumeTask(id: string): Promise<string>;
  CancelTask(id: string): Promise<void>;
  Ready(): Promise<void>;
}
declare global {
  interface Window {
    go?: { main: { App: API } };
  }
}
export function backend(): API {
  if (!window.go?.main?.App)
    throw new Error("请在跃传 Windows 桌面程序中使用此功能。");
  return window.go.main.App;
}
