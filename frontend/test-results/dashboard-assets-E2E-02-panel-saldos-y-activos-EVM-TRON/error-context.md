# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: dashboard-assets.spec.ts >> E2E-02: panel, saldos y activos EVM + TRON
- Location: e2e/dashboard-assets.spec.ts:3:1

# Error details

```
Error: Falta WALLET_E2E_EMAIL

expect(received).toBeTruthy()

Received: undefined
```

# Test source

```ts
  1   | import { test, expect } from '@playwright/test';
  2   | 
  3   | test('E2E-02: panel, saldos y activos EVM + TRON', async ({ page }) => {
  4   |   test.setTimeout(90000);
  5   | 
  6   |   const email = process.env.WALLET_E2E_EMAIL;
  7   |   const password = process.env.WALLET_E2E_PASSWORD;
  8   | 
> 9   |   expect(email, 'Falta WALLET_E2E_EMAIL').toBeTruthy();
      |                                           ^ Error: Falta WALLET_E2E_EMAIL
  10  |   expect(password, 'Falta WALLET_E2E_PASSWORD').toBeTruthy();
  11  | 
  12  |   const serverErrors: string[] = [];
  13  |   const pageErrors: string[] = [];
  14  | 
  15  |   page.on('response', response => {
  16  |     if (response.status() >= 500) {
  17  |       serverErrors.push(
  18  |         `${response.status()} ${new URL(response.url()).pathname}`
  19  |       );
  20  |     }
  21  |   });
  22  | 
  23  |   page.on('pageerror', error => pageErrors.push(error.message));
  24  | 
  25  |   await test.step('Autenticación y panel', async () => {
  26  |     await page.goto('/login', { waitUntil: 'domcontentloaded' });
  27  | 
  28  |     await page.locator(
  29  |       'input[type="email"], input[autocomplete="username"]'
  30  |     ).first().fill(email!);
  31  | 
  32  |     await page.locator('input[type="password"]').first().fill(password!);
  33  | 
  34  |     await page.getByRole('button', {
  35  |       name: /iniciar sesión|iniciar sesion|entrar|acceder|login|sign in/i
  36  |     }).click();
  37  | 
  38  |     try {
  39  |       await expect(page).toHaveURL(/\/dashboard\/?$/, { timeout: 30000 });
  40  |     } catch (error) {
  41  |       console.log('E2E-02 URL tras login:', page.url());
  42  |       console.log('E2E-02 mensaje visible:', (await page.locator('body').innerText()).slice(0, 2500));
  43  |       throw error;
  44  |     }
  45  |     await expect(page.getByText('Saldo total')).toBeVisible();
  46  |     await expect(page.getByText('Tus activos')).toBeVisible();
  47  | 
  48  |     console.log('E2E-02 panel:', page.url());
  49  |   });
  50  | 
  51  |   await test.step('Saldos y lista de activos', async () => {
  52  |     const balancesResponsePromise = page.waitForResponse(response =>
  53  |       new URL(response.url()).pathname === '/api/wallet/balances'
  54  |     );
  55  | 
  56  |     await page.goto('/assets', { waitUntil: 'domcontentloaded' });
  57  | 
  58  |     const response = await balancesResponsePromise;
  59  |     expect(
  60  |       response.status(),
  61  |       'El endpoint de saldos debe responder correctamente'
  62  |     ).toBeGreaterThanOrEqual(200);
  63  |     expect(response.status()).toBeLessThan(300);
  64  | 
  65  |     const data = await response.json();
  66  |     expect(
  67  |       Array.isArray(data?.balances),
  68  |       'La API debe devolver balances como una lista'
  69  |     ).toBe(true);
  70  | 
  71  |     for (const balance of data.balances) {
  72  |       expect(balance.asset, 'Cada saldo debe tener un activo').toBeTruthy();
  73  |       expect(Number.isFinite(Number(balance.balance))).toBe(true);
  74  |       expect(Number.isFinite(Number(balance.value_usdt))).toBe(true);
  75  |     }
  76  | 
  77  |     await expect(page.getByText('Mis activos')).toBeVisible();
  78  | 
  79  |     const assetsText = await page.locator('body').innerText();
  80  |     console.log('E2E-02 activos devueltos por API:', JSON.stringify(
  81  |       data.balances.map((b: any) => ({
  82  |         asset: b.asset,
  83  |         balance: b.balance,
  84  |         value_usdt: b.value_usdt
  85  |       }))
  86  |     ));
  87  |     console.log('E2E-02 pantalla de activos:', assetsText.slice(0, 2500));
  88  |   });
  89  | 
  90  |   await test.step('Redes anunciadas por el backend y visibles en pantalla', async () => {
  91  |     const networksResponsePromise = page.waitForResponse(response =>
  92  |       new URL(response.url()).pathname === '/api/wallet/networks'
  93  |     );
  94  | 
  95  |     await page.goto('/networks', { waitUntil: 'domcontentloaded' });
  96  | 
  97  |     const response = await networksResponsePromise;
  98  |     expect(response.status(), 'La API de redes debe responder 2xx')
  99  |       .toBeGreaterThanOrEqual(200);
  100 |     expect(response.status()).toBeLessThan(300);
  101 | 
  102 |     const data = await response.json();
  103 |     expect(Array.isArray(data?.networks),
  104 |       'La API debe devolver networks como una lista').toBe(true);
  105 | 
  106 |     const normalize = (value: unknown): string => {
  107 |       const raw = String(value ?? '').trim().toUpperCase();
  108 |       if (['TRON', 'TRX', 'TRC20', 'TRC-20'].includes(raw)) return 'TRON';
  109 |       if (['ETH', 'ETHEREUM', 'ERC20', 'ERC-20'].includes(raw)) return 'ETH';
```