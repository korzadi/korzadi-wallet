import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { registerSW } from 'virtual:pwa-register';
import './index.css';
import './kz.css';
import Landing from './pages/Landing';
import Buy from './pages/Buy';
import Networks from './pages/Networks';
import Transfer from './pages/Transfer';
import Login from './pages/Login';
import Register from './pages/Register';
import VerifyEmail from './pages/VerifyEmail';
import ForgotPassword from './pages/ForgotPassword';
import ResetPassword from './pages/ResetPassword';
import Dashboard from './pages/Dashboard';
import Send from './pages/Send';
import Receive from './pages/Receive';
import Deposits from './pages/Deposits';
import Withdrawals from './pages/Withdrawals';
import KYC from './pages/KYC';
import Security from './pages/Security';
import Assets from './pages/Assets';
import Transactions from './pages/Transactions';
import Profile from './pages/Profile';
import P2PMarket from './pages/p2p/Market';
import P2PCreateOffer from './pages/p2p/CreateOffer';
import P2PMyOffers from './pages/p2p/MyOffers';
import P2PMyOrders from './pages/p2p/MyOrders';
import P2POfferDetail from './pages/p2p/OfferDetail';
import P2POrderDetail from './pages/p2p/OrderDetail';



function ThemeInit() {
  const [initialized, setInitialized] = React.useState(false);
  React.useEffect(() => {
    const applyTheme = () => {
      const saved = localStorage.getItem('kz-th');
      const theme = saved || 'auto';
      document.documentElement.dataset.theme = theme;
      if (theme === 'auto' || !theme) {
        document.documentElement.removeAttribute('data-theme');
      } else {
        document.documentElement.setAttribute('data-theme', theme);
      }
      setInitialized(true);
    };
    applyTheme();
  }, []);
  return null;
}

registerSW({
  immediate: true,
  onNeedRefresh() {
    window.location.reload();
  },
});

function RequireAuth({ children }) {
  const token = localStorage.getItem('korzadi_access_token') || '';
  const isValidJWT = /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(token);
  return isValidJWT ? children : <Navigate to="/login" replace />;
}

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <ThemeInit />
    <BrowserRouter>
        <Routes>
          <Route path="/" element={<Navigate to="/WalletApp.html" replace />} />
          <Route path="/login" element={<Login />} />
          <Route path="/register" element={<Register />} />
          <Route path="/verify-email" element={<VerifyEmail />} />
          <Route path="/forgot-password" element={<ForgotPassword />} />
          <Route path="/reset-password" element={<ResetPassword />} />
          <Route path="/dashboard" element={<RequireAuth><Dashboard /></RequireAuth>} />
          <Route path="/assets" element={<RequireAuth><Assets /></RequireAuth>} />
          <Route path="/transactions" element={<RequireAuth><Transactions /></RequireAuth>} />
          <Route path="/profile" element={<RequireAuth><Profile /></RequireAuth>} />
          <Route path="/buy" element={<RequireAuth><Buy /></RequireAuth>} />
          <Route path="/networks" element={<RequireAuth><Networks /></RequireAuth>} />
          <Route path="/transfer" element={<RequireAuth><Transfer /></RequireAuth>} />
          <Route path="/send" element={<RequireAuth><Send /></RequireAuth>} />
          <Route path="/receive" element={<RequireAuth><Receive /></RequireAuth>} />
          <Route path="/deposits" element={<RequireAuth><Deposits /></RequireAuth>} />
          <Route path="/withdraw" element={<Navigate to="/send" replace />} />
          <Route path="/withdrawals" element={<RequireAuth><Withdrawals /></RequireAuth>} />
          <Route path="/kyc" element={<RequireAuth><KYC /></RequireAuth>} />
          <Route path="/security" element={<RequireAuth><Security /></RequireAuth>} />
          <Route path="/p2p" element={<RequireAuth><P2PMarket /></RequireAuth>} />
          <Route path="/p2p/market" element={<RequireAuth><P2PMarket /></RequireAuth>} />
          <Route path="/p2p/create-offer" element={<RequireAuth><P2PCreateOffer /></RequireAuth>} />
          <Route path="/p2p/my-offers" element={<RequireAuth><P2PMyOffers /></RequireAuth>} />
          <Route path="/p2p/my-orders" element={<RequireAuth><P2PMyOrders /></RequireAuth>} />
          <Route path="/p2p/offers/:id" element={<RequireAuth><P2POfferDetail /></RequireAuth>} />
          <Route path="/p2p/orders/:id" element={<RequireAuth><P2POrderDetail /></RequireAuth>} />

          <Route path="/wallet" element={<Navigate to="/WalletApp.html" replace />} />

          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
  </React.StrictMode>
);
