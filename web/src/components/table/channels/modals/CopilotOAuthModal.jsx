import React, { useEffect, useState, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Modal,
  Button,
  Space,
  Typography,
  Banner,
  Spin,
} from '@douyinfe/semi-ui';
import { API, showError, showSuccess } from '../../../../helpers';

const { Text, Title } = Typography;

const CopilotOAuthModal = ({ visible, onCancel, onSuccess, channelId }) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [deviceInfo, setDeviceInfo] = useState(null);
  const [polling, setPolling] = useState(false);
  const pollingRef = useRef(null);

  const startDeviceFlow = async () => {
    setLoading(true);
    setDeviceInfo(null);
    try {
      const url = channelId
        ? `/api/channel/${channelId}/copilot/oauth/start`
        : '/api/channel/copilot/oauth/start';
      const res = await API.post(url, {}, { skipErrorHandler: true });
      if (!res?.data?.success) {
        throw new Error(res?.data?.message || t('启动授权失败'));
      }
      const data = res.data.data;
      setDeviceInfo(data);
      setPolling(true);
      startPolling(data.device_code, data.interval || 5);
    } catch (error) {
      showError(error?.message || t('启动授权失败'));
    } finally {
      setLoading(false);
    }
  };

  const startPolling = (deviceCode, interval) => {
    if (pollingRef.current) {
      clearInterval(pollingRef.current);
    }
    let pollInterval = interval * 1000;

    const poll = async () => {
      try {
        const url = channelId
          ? `/api/channel/${channelId}/copilot/oauth/poll`
          : '/api/channel/copilot/oauth/poll';
        const res = await API.post(
          url,
          { device_code: deviceCode },
          { skipErrorHandler: true },
        );

        if (!res?.data?.success) {
          stopPolling();
          showError(res?.data?.message || t('授权失败'));
          return;
        }

        const data = res.data.data;
        if (data.status === 'authorized') {
          stopPolling();
          if (data.access_token) {
            onSuccess && onSuccess(data.access_token, data.user_name);
          } else {
            // Token was saved directly to channel
            onSuccess && onSuccess(null, data.user_name);
          }
          showSuccess(
            data.user_name
              ? t('GitHub 账户授权成功') + `: ${data.user_name}`
              : t('GitHub 账户授权成功'),
          );
          onCancel && onCancel();
          return;
        }

        if (data.status === 'slow_down') {
          // Increase poll interval
          stopPolling();
          pollInterval = (data.interval || 10) * 1000;
          pollingRef.current = setInterval(poll, pollInterval);
        }
        // status === 'pending': keep polling
      } catch (error) {
        // Network error, keep trying
      }
    };

    pollingRef.current = setInterval(poll, pollInterval);
  };

  const stopPolling = () => {
    if (pollingRef.current) {
      clearInterval(pollingRef.current);
      pollingRef.current = null;
    }
    setPolling(false);
  };

  useEffect(() => {
    if (!visible) {
      stopPolling();
      setDeviceInfo(null);
    }
  }, [visible]);

  useEffect(() => {
    return () => stopPolling();
  }, []);

  return (
    <Modal
      title={t('GitHub Copilot 授权')}
      visible={visible}
      onCancel={() => {
        stopPolling();
        onCancel && onCancel();
      }}
      maskClosable={false}
      closeOnEsc
      width={520}
      footer={
        <Space>
          <Button
            theme='borderless'
            onClick={() => {
              stopPolling();
              onCancel && onCancel();
            }}
          >
            {t('取消')}
          </Button>
          {!deviceInfo && (
            <Button
              theme='solid'
              type='primary'
              onClick={startDeviceFlow}
              loading={loading}
            >
              {t('开始授权')}
            </Button>
          )}
        </Space>
      }
    >
      <Space vertical spacing='medium' style={{ width: '100%' }}>
        {!deviceInfo && (
          <Banner
            type='info'
            description={t(
              '点击「开始授权」后，会获得一个授权码。在浏览器中打开 GitHub 页面输入该授权码完成授权。',
            )}
          />
        )}

        {deviceInfo && (
          <>
            <Banner
              type='warning'
              description={t(
                '请在浏览器中打开下方链接，并输入授权码完成授权。授权完成后会自动获取 Token。',
              )}
            />

            <div
              style={{
                textAlign: 'center',
                padding: '16px',
                background: 'var(--semi-color-fill-0)',
                borderRadius: '8px',
              }}
            >
              <Text type='tertiary'>{t('授权码')}</Text>
              <Title
                heading={2}
                style={{
                  letterSpacing: '4px',
                  margin: '8px 0',
                  fontFamily: 'monospace',
                }}
              >
                {deviceInfo.user_code}
              </Title>
              <Button
                type='primary'
                theme='solid'
                onClick={() =>
                  window.open(
                    deviceInfo.verification_uri,
                    '_blank',
                    'noopener,noreferrer',
                  )
                }
              >
                {t('打开 GitHub 授权页面')}
              </Button>
            </div>

            {polling && (
              <div style={{ textAlign: 'center', padding: '8px' }}>
                <Spin size='middle' />
                <Text
                  type='tertiary'
                  style={{ display: 'block', marginTop: '8px' }}
                >
                  {t('等待授权中...')}
                </Text>
              </div>
            )}
          </>
        )}
      </Space>
    </Modal>
  );
};

export default CopilotOAuthModal;
