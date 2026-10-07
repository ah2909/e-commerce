import { readFileSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { randomUUID } from "node:crypto";
import { request } from "node:http";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const subdomain = process.argv[2] || "demo1007";
if (!/^[a-z][a-z0-9-]{2,30}$/.test(subdomain)) throw new Error("Use a lowercase demo subdomain of 3–31 characters.");
const cache = resolve(root, ".cache");
mkdirSync(cache, { recursive: true });
const manifestPath = resolve(cache, `${subdomain}.json`);
const existing = existsSync(manifestPath);
const state = existing ? JSON.parse(readFileSync(manifestPath, "utf8")) : {
  subdomain, host: `${subdomain}.example.test`, ownerEmail: `owner@${subdomain}.test`,
  shopperEmail: `customer@${subdomain}.test`, password: `Demo-${randomUUID()}!`, products: [],
};
if (state.host !== `${subdomain}.example.test`) throw new Error("Demo manifest host does not match.");
const save = () => writeFileSync(manifestPath, JSON.stringify(state, null, 2));
const envPath = resolve(root, existsSync(resolve(root, ".env")) ? ".env" : ".env.example");
const localEnv = Object.fromEntries(readFileSync(envPath, "utf8").split(/\r?\n/).filter(line => /^[A-Z][A-Z0-9_]*=/.test(line)).map(line => {
  const index = line.indexOf("="); return [line.slice(0, index), line.slice(index + 1)];
}));
const platformToken = process.env.PLATFORM_ADMIN_TOKEN || localEnv.PLATFORM_ADMIN_TOKEN;
if (!platformToken) throw new Error("PLATFORM_ADMIN_TOKEN is required.");
async function api(host, path, method = "GET", body, token, allowed = []) {
  const response = await new Promise((accept, reject) => {
    const req = request(`http://127.0.0.1:8080${path}`, {
      method, signal: AbortSignal.timeout(15000), headers: {
      Host: host, "Content-Type": "application/json", "Idempotency-Key": randomUUID(),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    }, res => {
      let text = "";
      res.setEncoding("utf8");
      res.on("data", chunk => { text += chunk; });
      res.on("error", reject);
      res.on("end", () => {
        try { accept({ status: res.statusCode, data: JSON.parse(text) }); }
        catch { reject(new Error(`${method} ${path}: invalid JSON response`)); }
      });
    });
    req.on("error", reject);
    req.end(body === undefined ? undefined : JSON.stringify(body));
  });
  const { data } = response;
  if (response.status >= 400 && !allowed.includes(response.status)) throw new Error(`${method} ${path}: ${response.status} ${data.code || "request failed"}`);
  return { data, status: response.status };
}
if (!state.storeID) {
  save();
  const { data } = await api("example.test", "/api/v1/onboarding/stores", "POST", {
    subdomain, store_name: "Cửa hàng thử nghiệm", email: state.ownerEmail, password: state.password,
  }, platformToken);
  state.storeID = data.store_id; save();
}
const { data: owner } = await api(state.host, "/api/v1/auth/sessions", "POST", { email: state.ownerEmail, password: state.password });
const ownerToken = owner.access_token;
const admin = (path, method = "GET", body, allowed = []) => api(state.host, `/api/v1/admin${path}`, method, body, ownerToken, allowed);
if (!state.categoryID) {
  const { data } = await admin("/catalog/categories", "POST", { name: "Sản phẩm demo", slug: "san-pham-demo" });
  state.categoryID = data.category_id; save();
}
const fixtures = [
  { name: "Áo thể thao Demo", slug: "ao-the-thao-demo", sku: "DEMO-AO-M", price: 125000, stock: 30, weight: 300 },
  { name: "Túi vải Demo", slug: "tui-vai-demo", sku: "DEMO-TUI", price: 89000, stock: 20, weight: 200 },
  { name: "Bình nước Demo", slug: "binh-nuoc-demo", sku: "DEMO-BINH", price: 159000, stock: 15, weight: 400 },
];
for (const fixture of fixtures) {
  if (state.products.some(product => product.slug === fixture.slug)) continue;
  const { data } = await admin("/catalog/products", "POST", {
    name: fixture.name, slug: fixture.slug, description: "Sản phẩm và số lượng giả lập để kiểm thử cửa hàng.",
    category_ids: [state.categoryID], media: [], variants: [{ sku: fixture.sku, option_values: {}, weight_grams: fixture.weight, dimensions: {} }],
  });
  state.products.push({ ...fixture, productID: data.product_id, variantID: data.variants[0].variant_id, version: data.version }); save();
}
if (!state.priceListID) {
  const { data } = await admin("/pricing/price-lists", "POST", {
    name: "Giá demo", channel: "storefront", currency: "VND", priority: 1,
    prices: state.products.map(product => ({ variant_id: product.variantID, amount_minor: product.price })),
  });
  state.priceListID = data.price_list_id; save();
}
for (const product of state.products) {
  if (!product.stocked) {
    await admin(`/inventory/stock-items/${product.variantID}/adjustments`, "POST", { quantity_delta: product.stock, reason: "Local demo fixture", expected_version: 0 });
    product.stocked = true; save();
  }
  if (!product.published) {
    await admin(`/catalog/products/${product.productID}/publish`, "POST", { expected_version: product.version });
    product.published = true; save();
  }
}
if (!state.configured) {
  const payment = await admin("/payment-providers/vietqr", "GET", undefined, [404]);
  await admin("/payment-providers/vietqr", "PUT", { expected_version: payment.status === 200 ? payment.data.version : 0, enabled: true, bank_bin: "970436", account_number: "000000000000", account_name: "DEMO ONLY DO NOT TRANSFER" });
  const shipping = await admin("/shipping/rule", "GET", undefined, [404]);
  await admin("/shipping/rule", "PUT", { expected_version: shipping.status === 200 ? shipping.data.version : 0, mode: "free", flat_amount_minor: 0 });
  const { data: appearance } = await admin("/storefront");
  await admin("/storefront", "PUT", {
    expected_version: appearance.version, display_name: "Cửa hàng Demo", tagline: "Dữ liệu mẫu để bạn kiểm thử",
    logo_url: "", hero_url: "", hero_title: "Khám phá cửa hàng thử nghiệm", hero_text: "Ba sản phẩm mẫu, thanh toán VietQR giả lập và miễn phí vận chuyển.",
    announcement: "DEMO — không chuyển tiền thật", preset: "sport",
  });
  state.configured = true; save();
}
let shopperToken;
if (!state.shopperCreated) {
  const { data } = await api(state.host, "/api/v1/auth/registrations", "POST", { email: state.shopperEmail, password: state.password });
  shopperToken = data.access_token; state.shopperCreated = true; save();
} else {
  const { data } = await api(state.host, "/api/v1/auth/sessions", "POST", { email: state.shopperEmail, password: state.password });
  shopperToken = data.access_token;
}
if (!state.profileCreated) {
  await api(state.host, "/api/v1/customer/profile", "PUT", { expected_version: 0, full_name: "Khách hàng Demo", email: state.shopperEmail, phone: "0900000000" }, shopperToken);
  state.profileCreated = true; save();
}
if (!state.addressID) {
  const { data } = await api(state.host, "/api/v1/customer/addresses", "POST", {
    label: "Địa chỉ thử nghiệm", recipient_name: "Khách hàng Demo", phone: "0900000000", line1: "123 Đường Demo",
    line2: "", ward: "Phường Sài Gòn", district: "", province: "Thành phố Hồ Chí Minh", postal_code: "", country_code: "VN",
  }, shopperToken);
  state.addressID = data.address_id; save();
}
const { data: catalog } = await api(state.host, "/api/v1/catalog/products");
const count = catalog.products.length;
if (count !== fixtures.length) throw new Error(`Expected ${fixtures.length} public products, got ${count}.`);
const denied = await api("shop.example.test", "/api/v1/admin/catalog/products", "GET", undefined, ownerToken, [401, 403]);
if (![401, 403].includes(denied.status)) throw new Error("Cross-store credential unexpectedly accepted.");
state.complete = true; save();
console.log(`Demo ready: http://${state.host}:3000 (${count} public products)`);
console.log(`Local credentials and identifiers: ${manifestPath}`);
