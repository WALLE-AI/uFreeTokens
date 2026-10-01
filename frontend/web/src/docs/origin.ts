import { GATEWAY_BASE_URL } from '../api/client';

// 文档示例里 {{BASE_URL}} 替换成的地址：显式配置了网关地址就用它，否则同源
// （dev 走 Vite proxy、prod 走 Nginx 反代），所以 dev/预发/生产各自显示正确的地址。
export function apiOrigin(): string {
  return GATEWAY_BASE_URL || window.location.origin;
}
