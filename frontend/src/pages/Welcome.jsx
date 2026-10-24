import { Link, Navigate } from 'react-router-dom';

export default function Welcome() {
  if (localStorage.getItem('korzadi_access_token')) return <Navigate to="/dashboard" replace />;
  return (
    <div className="kz-welcome">
      <div className="big">K</div>
      <h1>Korzadi <b>Wallet</b></h1>
      <h2>Tu dinero. Tu control.</h2>
      <p>Envía, recibe, intercambia y administra tus criptomonedas de forma segura y sencilla.</p>
      <div className="btns">
        <Link to="/login" className="kz-btn">Iniciar sesión</Link>
        <Link to="/register" className="kz-btn ghost">Crear cuenta</Link>
      </div>
      <small>Korzadi Wallet v1.0</small>
    </div>
  );
}
