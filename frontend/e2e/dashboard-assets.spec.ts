import { test, expect } from '@playwright/test';

test('E2E-02: panel, saldos y activos EVM + TRON', async ({ page }) => {
  test.setTimeout(90000);

  const email = process.env.WALLET_E2E_EMAIL;
  const password = process.env.WALLET_E2E_PASSWORD;

  expect(email, 'Falta WALLET_E2E_EMAIL').toBeTruthy();
  expect(password, 'Falta WALLET_E2E_PASSWORD').toBeTruthy();

  const serverErrors: string[] = [];
  const pageErrors: string[] = [];

  page.on('response', response => {
    if (response.status() >= 500) {
      serverErrors.push(
        `${response.status()} ${new URL(response.url()).pathname}`
      );
    }
  });

  page.on('pageerror', error => pageErrors.push(error.message));

  await test.step('Autenticación y panel', async () => {
    await page.goto('/login', { waitUntil: 'domcontentloaded' });

    await page.locator(
      'input[type="email"], input[autocomplete="username"]'
    ).first().fill(email!);

    await page.locator('input[type="password"]').first().fill(password!);

    await page.getByRole('button', {
      name: /iniciar sesión|iniciar sesion|entrar|acceder|login|sign in/i
    }).click();

    try {
      await expect(page).toHaveURL(/\/dashboard\/?$/, { timeout: 30000 });
    } catch (error) {
      console.log('E2E-02 URL tras login:', page.url());
      console.log('E2E-02 mensaje visible:', (await page.locator('body').innerText()).slice(0, 2500));
      throw error;
    }
    await expect(page.getByText('Saldo total')).toBeVisible();
    await expect(page.getByText('Tus activos')).toBeVisible();

    console.log('E2E-02 panel:', page.url());
  });

  await test.step('Saldos y lista de activos', async () => {
    const balancesResponsePromise = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/wallet/balances'
    );

    await page.goto('/assets', { waitUntil: 'domcontentloaded' });

    const response = await balancesResponsePromise;
    expect(
      response.status(),
      'El endpoint de saldos debe responder correctamente'
    ).toBeGreaterThanOrEqual(200);
    expect(response.status()).toBeLessThan(300);

    const data = await response.json();
    expect(
      Array.isArray(data?.balances),
      'La API debe devolver balances como una lista'
    ).toBe(true);

    for (const balance of data.balances) {
      expect(balance.asset, 'Cada saldo debe tener un activo').toBeTruthy();
      expect(Number.isFinite(Number(balance.balance))).toBe(true);
      expect(Number.isFinite(Number(balance.value_usdt))).toBe(true);
    }

    await expect(page.getByText('Mis activos')).toBeVisible();

    const assetsText = await page.locator('body').innerText();
    console.log('E2E-02 activos devueltos por API:', JSON.stringify(
      data.balances.map((b: any) => ({
        asset: b.asset,
        balance: b.balance,
        value_usdt: b.value_usdt
      }))
    ));
    console.log('E2E-02 pantalla de activos:', assetsText.slice(0, 2500));
  });

  await test.step('Redes anunciadas por el backend y visibles en pantalla', async () => {
    const networksResponsePromise = page.waitForResponse(response =>
      new URL(response.url()).pathname === '/api/wallet/networks'
    );

    await page.goto('/networks', { waitUntil: 'domcontentloaded' });

    const response = await networksResponsePromise;
    expect(response.status(), 'La API de redes debe responder 2xx')
      .toBeGreaterThanOrEqual(200);
    expect(response.status()).toBeLessThan(300);

    const data = await response.json();
    expect(Array.isArray(data?.networks),
      'La API debe devolver networks como una lista').toBe(true);

    const normalize = (value: unknown): string => {
      const raw = String(value ?? '').trim().toUpperCase();
      if (['TRON', 'TRX', 'TRC20', 'TRC-20'].includes(raw)) return 'TRON';
      if (['ETH', 'ETHEREUM', 'ERC20', 'ERC-20'].includes(raw)) return 'ETH';
      if (['BSC', 'BNB', 'BNB SMART CHAIN', 'BINANCE SMART CHAIN',
           'BEP20', 'BEP-20'].includes(raw)) return 'BSC';
      return raw;
    };

    const networkCodes = data.networks.map((item: any) =>
      normalize(typeof item === 'string'
        ? item
        : item.network ?? item.code ?? item.name)
    );

    console.log('E2E-02 redes devueltas por API:', JSON.stringify(networkCodes));

    for (const required of ['ETH', 'BSC', 'TRON']) {
      expect(
        networkCodes,
        `El backend no anuncia la red requerida: ${required}`
      ).toContain(required);
    }

    await expect(page.getByText('Selecciona una red')).toBeVisible();

    const body = (await page.locator('body').innerText()).toUpperCase();

    for (const [label, pattern] of [
      ['Ethereum ERC-20', /Ethereum\s*\(ERC20\)/i],
      ['BSC BEP-20', /BSC\s*\(BEP20\)/],
      ['TRON TRC-20', /TRON\s*\(TRC20\)/],
    ] as const) {
      expect(body, `No aparece ${label} en la pantalla de redes`)
        .toMatch(pattern);
    }
  });

  await test.step('Errores del navegador y del servidor', async () => {
    console.log('E2E-02 HTTP 5xx:', JSON.stringify(serverErrors));
    console.log('E2E-02 errores JavaScript:', JSON.stringify(pageErrors));

    expect(serverErrors).toEqual([]);
    expect(pageErrors).toEqual([]);
  });

  console.log('E2E-02 PASS: panel, saldos, EVM y TRON');
});
