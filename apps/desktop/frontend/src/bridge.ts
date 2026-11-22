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
}
export interface Task {
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
}
interface API {
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
