import React, { useState } from 'react';
import AdminActions from './components/AdminActions';

import { Layout, Menu } from 'antd';

const { Sider, Content } = Layout;

function App() {
    const [activePage, setActivePage] = useState('Admin');
    const handleMenuClick = (e) => {
        setActivePage(e.key);
    };

    return (
        <Layout style={{ minHeight: '100vh' }}>
            <Sider width={200} className="site-layout-background">
                <Menu
                    mode="inline"
                    selectedKeys={[activePage]} // Keep track of the selected page
                    style={{ height: '100%', borderRight: 0 }}
                    onClick={handleMenuClick}
                >
                    <Menu.Item key="Admin">Log PipeLine Admin</Menu.Item>
                </Menu>
            </Sider>
            <Layout style={{ padding: '0 24px 24px' }}>
                <Content
                    style={{
                        padding: 24,
                        margin: 0,
                        minHeight: 280,
                    }}
                >
                    <div style={{ display: activePage === 'Admin' ? 'block' : 'none' }}>
                       <AdminActions />
                    </div>
                </Content>
            </Layout>
        </Layout>
    );
}

export default App;
