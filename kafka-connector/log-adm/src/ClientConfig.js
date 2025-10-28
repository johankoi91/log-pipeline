import { useEffect, useState } from "react";

export const ClientConfig = {
  ready: false,
  value: {
    api_base_url: ""
  },
};

/**
 * 运行时加载 public/client-config.json
 * - 默认从 /client-config.json 读
 * - 加了时间戳避免缓存
 * - 幂等：多次调用只会第一次真正请求
 */
export async function loadClientConfig() {
  if (ClientConfig.ready) return ClientConfig.value;

  const res = await fetch('/client-config.json', { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`加载配置失败：${res.status} ${res.statusText}`);
  }

  const json = await res.json();
  ClientConfig.value = {
    api_base_url: String(json.api_base_url || "").trim(),
  };
  ClientConfig.ready = true;

  return ClientConfig.value;
}

export function useClientConfigReady() {
  const [ready, setReady] = useState(ClientConfig.ready);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (ClientConfig.ready) return; // 已加载过，跳过

    let alive = true;
    (async () => {
      try {
        await loadClientConfig();
        if (alive) setReady(true);   // 成功 -> ready
      } catch (e) {
        if (alive) setError(e);
      }
    })();

    return () => { alive = false; };
  }, []);

  return { ready, error, cfg: ClientConfig.value };
}

export const resolveUrl = (base, path) =>
  `${String(base || "").replace(/\/+$/, "")}${path}`;



/***
 * 在 React 中，useEffect 的执行顺序取决于组件的渲染顺序。你提到的两个 useEffect，分别是在两个不同的函数组件中：

useClientConfigReady 里的 useEffect：这个 useEffect 是负责加载客户端配置（api_base_url 等）。

AdminActions 里的 useEffect：这个 useEffect 主要做两件事：

它等待 useClientConfigReady 提供的 cfg.api_base_url。

然后，它在 API 加载完成后探测 connector 状态。

执行顺序：

useClientConfigReady 先执行，因为它是负责异步加载配置的 hook：

当 AdminActions 被渲染时，useClientConfigReady 会先被调用。

在 useClientConfigReady 内的 useEffect 中，配置会被异步加载。在此过程中，cfg 的 api_base_url 可能是 undefined，直到配置加载完成。

AdminActions 里的 useEffect 执行：

由于 AdminActions 调用了 useClientConfigReady，它会等待 cfg 和 api_base_url 的加载结果。

在 AdminActions 的 useEffect 中，它会检测 cfg.api_base_url 是否可用。

如果 cfg.api_base_url 还没有准备好，useEffect 不会继续执行。

一旦 cfg 加载完毕，AdminActions 组件会重新渲染，useEffect 会再次执行，从而开始进行 connector 状态的探测。

具体执行流程：

useClientConfigReady 的 useEffect 执行：

首先，它尝试异步加载配置。

组件第一次渲染时，cfg 中的 api_base_url 可能是 undefined，然后会开始异步加载配置。

当配置加载完成后，cfg.api_base_url 会有值，ready 会设置为 true。

AdminActions 组件渲染时执行 useEffect：

AdminActions 首次渲染时，cfg 可能还是 undefined（即 useClientConfigReady 还没有加载配置）。

一旦 useClientConfigReady 里的 useEffect 加载完配置并更新 cfg，React 会重新渲染 AdminActions 组件。

这时，AdminActions 的 useEffect 会检测到 cfg.api_base_url 已经有值，然后开始发起请求。

图示：
1. 组件加载：
   - AdminActions -> 调用 useClientConfigReady -> useClientConfigReady 的 useEffect 开始执行
   - useClientConfigReady 里面的 useEffect 执行，配置开始加载（cfg.api_base_url 为空）

2. 配置加载完成：
   - useClientConfigReady 的 useEffect 设置 cfg 和 ready
   - AdminActions 重新渲染，检测到 cfg.api_base_url 有值

3. AdminActions 的 useEffect 执行：
   - 由于 cfg.api_base_url 可用，开始进行 fetch 请求

小结：

useClientConfigReady 先执行，它异步加载配置并在完成时更新 cfg。

AdminActions 的 useEffect 在 cfg 更新后执行，如果 cfg.api_base_url 存在，它才会进行相关的网络请求。

因此，AdminActions 的 useEffect 会等到 useClientConfigReady 里的配置加载完成后，再执行。
 * 
 * 
 */