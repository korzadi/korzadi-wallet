import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { registerSW } from 'virtual:pwa-register';
import './index.css';
import './kz.css';
import Welcome from './pages/Welcome';
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
import Admin from './pages/admin/Admin';
import AdminAdvanced from './pages/admin/AdminAdvanced';
import P2PMarket from './pages/p2p/Market';
import P2PCreateOffer from './pages/p2p/CreateOffer';
import P2PMyOffers from './pages/p2p/MyOffers';
import P2PMyOrders from './pages/p2p/MyOrders';
import P2POfferDetail from './pages/p2p/OfferDetail';
import P2POrderDetail from './pages/p2p/OrderDetail';



registerSW({
  immediate: true,
  onNeedRefresh() {
    window.location.reload();
  },
});

function RequireAuth({ children }) {
  const hasToken = !!localStorage.getItem('korzadi_access_token');
  return hasToken
    ? children
    : <Navigate to="/login" replace />;
}

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Welcome />} />
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
        <Route path="/admin" element={<RequireAuth><Admin /></RequireAuth>} />
        <Route path="/admin/users" element={<RequireAuth><Admin /></RequireAuth>} />
        <Route path="/admin/transactions" element={<RequireAuth><Admin /></RequireAuth>} />
        <Route path="/admin/audit" element={<RequireAuth><Admin /></RequireAuth>} />
        <Route path="/admin/withdrawals" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/custody" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/hot-wallet" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/treasury" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/gas" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/p2p" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/security" element={<RequireAuth><AdminAdvanced /></RequireAuth>} />
        <Route path="/admin/profile" element={<RequireAuth><Admin /></RequireAuth>} />

        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  </React.StrictMode>
);
