import http from "k6/http";
import { check, sleep } from "k6";

const vus = Number(__ENV.LOAD_VUS || "5");
if (!Number.isInteger(vus) || vus < 1 || vus > 50) {
  throw new Error("LOAD_VUS must be between 1 and 50");
}

export const options = {
  vus,
  duration: "2m",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<500"],
  },
};

const baseURL = __ENV.LOAD_BASE_URL || "http://127.0.0.1:8080";
const host = __ENV.LOAD_STORE_HOST || "shop.example.test";

export default function () {
  const response = http.get(`${baseURL}/api/v1/catalog/products?limit=20`, { headers: { Host: host } });
  check(response, { "catalog is successful": (value) => value.status === 200 });
  sleep(1);
}
