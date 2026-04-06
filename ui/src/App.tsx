import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import AuthProvider from './auth/AuthContext';
import { useAuth } from './auth/useAuth';
import LoginPage from './auth/LoginPage';
import Layout from './components/Layout';
import DeviceList from './pages/devices/DeviceList';
import DeviceDetail from './pages/devices/DeviceDetail';
import AddDevice from './pages/devices/AddDevice';
import GroupsTagsSites from './pages/devices/GroupsTagsSites';
import JobList from './pages/jobs/JobList';
import JobDetail from './pages/jobs/JobDetail';
import JobCreate from './pages/jobs/JobCreate';
import ScheduledJobs from './pages/scheduled/ScheduledJobs';
import FileManager from './pages/files/FileManager';
import AdminLayout from './pages/admin/AdminLayout';
import Users from './pages/admin/Users';
import Roles from './pages/admin/Roles';
import ApiKeys from './pages/admin/ApiKeys';
import EnrollmentTokens from './pages/admin/EnrollmentTokens';
import AlertRules from './pages/admin/AlertRules';
import TenantSettings from './pages/admin/TenantSettings';
import AuditLog from './pages/admin/AuditLog';

function AppRoutes() {
  const { isAuthenticated } = useAuth();

  if (!isAuthenticated) return <LoginPage />;

  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<Navigate to="/devices" replace />} />

        {/* Devices */}
        <Route path="/devices" element={<DeviceList />} />
        <Route path="/devices/add" element={<AddDevice />} />
        <Route path="/devices/organize" element={<GroupsTagsSites />} />
        <Route path="/devices/:deviceId" element={<DeviceDetail />} />

        {/* Jobs */}
        <Route path="/jobs" element={<JobList />} />
        <Route path="/jobs/new" element={<JobCreate />} />
        <Route path="/jobs/:jobId" element={<JobDetail />} />

        {/* Scheduled */}
        <Route path="/scheduled" element={<ScheduledJobs />} />

        {/* Files */}
        <Route path="/files" element={<FileManager />} />

        {/* Admin */}
        <Route path="/admin" element={<AdminLayout />}>
          <Route index element={<Navigate to="/admin/users" replace />} />
          <Route path="users" element={<Users />} />
          <Route path="roles" element={<Roles />} />
          <Route path="api-keys" element={<ApiKeys />} />
          <Route path="enrollment-tokens" element={<EnrollmentTokens />} />
          <Route path="alerts" element={<AlertRules />} />
          <Route path="tenant" element={<TenantSettings />} />
          <Route path="audit" element={<AuditLog />} />
        </Route>

        <Route path="*" element={<Navigate to="/devices" replace />} />
      </Route>
    </Routes>
  );
}

export default function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <AppRoutes />
      </BrowserRouter>
    </AuthProvider>
  );
}
