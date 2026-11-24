/*
 * BitSrunLoginGo control panel - LuCI flavoured, no framework, no build step.
 *
 * The whole UI is three files served from the Go binary: this script, the
 * stylesheet and index.html. Writing it by hand keeps `go build` the only
 * build step in the repository, which is what lets the CI workflow produce a
 * binary and a package without a node toolchain in the image.
 */
(function () {
  'use strict';

  // --- tiny helpers -------------------------------------------------------

  function $(sel, root) {
    return (root || document).querySelector(sel);
  }

  function $$(sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  }

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    if (attrs) {
      Object.keys(attrs).forEach(function (key) {
        var value = attrs[key];
        if (value === null || value === undefined || value === false) return;
        if (key === 'text') {
          node.textContent = value;
        } else if (key === 'html') {
          node.innerHTML = value;
        } else if (key.indexOf('on') === 0 && typeof value === 'function') {
          node.addEventListener(key.slice(2).toLowerCase(), value);
        } else if (key === 'class') {
          node.className = value;
        } else {
          node.setAttribute(key, value === true ? '' : value);
        }
      });
    }
    (children || []).forEach(function (child) {
      if (child === null || child === undefined) return;
      node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
    });
    return node;
  }

  function fmtDuration(seconds) {
    seconds = Number(seconds) || 0;
    if (seconds < 60) return seconds + ' 秒';
    if (seconds % 3600 === 0) return seconds / 3600 + ' 小时';
    if (seconds % 60 === 0) return seconds / 60 + ' 分钟';
    return seconds + ' 秒';
  }

  function fmtTime(value) {
    if (!value) return '—';
    var d = new Date(value);
    if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '—';
    var pad = function (n) {
      return n < 10 ? '0' + n : String(n);
    };
    return (
      d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' +
      pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds())
    );
  }

  // --- api ----------------------------------------------------------------

  // basePath is the path the page is served under: "" when opened directly,
  // "/app/bitsrunlogin-go" behind the fnOS gateway. Request paths are relative
  // to it so the same page works in both cases.
  //
  // The server injects it because the document's own URL is not a reliable
  // source: a deep link like /app/bitsrunlogin-go/logs has the route as its
  // last segment, and guessing from it produced requests for /app/api/... .
  // It is read from the <base> tag the shell carries, falling back to the
  // script URL for an html file served without the injection.
  var basePath = (function () {
    if (typeof window.__BS_BASE__ === 'string') {
      return window.__BS_BASE__.replace(/\/+$/, '');
    }
    var base = document.querySelector('base');
    if (base && base.getAttribute('href')) {
      return base.getAttribute('href').replace(/\/+$/, '');
    }
    return '';
  })();

  var api = {
    request: function (method, path, body) {
      var init = { method: method, headers: { Accept: 'application/json' } };
      if (body !== undefined) {
        init.headers['Content-Type'] = 'application/json';
        init.body = JSON.stringify(body);
      }
      return fetch(basePath + path, init).then(function (res) {
        return res.text().then(function (text) {
          var data = null;
          if (text) {
            try {
              data = JSON.parse(text);
            } catch (e) {
              data = { error: text };
            }
          }
          if (!res.ok) {
            var message = (data && (data.error || data.message)) || res.statusText;
            var err = new Error(message);
            err.status = res.status;
            throw err;
          }
          return data;
        });
      });
    },
    status: function () {
      return api.request('GET', '/api/status');
    },
    config: function () {
      return api.request('GET', '/api/config');
    },
    saveConfig: function (patch) {
      return api.request('PUT', '/api/config', patch);
    },
    version: function () {
      return api.request('GET', '/api/version');
    },
    logs: function (after, limit) {
      var qs = '/api/logs?limit=' + (limit || 200);
      if (after) qs += '&after=' + after;
      return api.request('GET', qs);
    },
    service: function (action) {
      if (!action) return api.request('GET', '/api/service');
      return api.request('POST', '/api/service/' + action);
    },
    loginNow: function () {
      return api.request('POST', '/api/login');
    }
  };

  // --- notifications ------------------------------------------------------

  function notify(message, kind) {
    var box = $('#notify');
    if (!box) return;
    var item = el('div', { class: 'toast toast-' + (kind || 'info') }, [message]);
    box.appendChild(item);
    setTimeout(function () {
      item.classList.add('toast-out');
      setTimeout(function () {
        if (item.parentNode) item.parentNode.removeChild(item);
      }, 300);
    }, kind === 'error' ? 6000 : 3000);
  }

  // --- form helpers -------------------------------------------------------

  // field renders one LuCI style row: label on the left, control on the right.
  function field(label, control, help) {
    return el('div', { class: 'field' }, [
      el('label', { class: 'field-label' }, [label]),
      el('div', { class: 'field-body' }, [control, help ? el('div', { class: 'help' }, [help]) : null])
    ]);
  }

  function input(type, value, attrs) {
    var a = { type: type, value: value === undefined || value === null ? '' : String(value) };
    Object.keys(attrs || {}).forEach(function (k) {
      a[k] = attrs[k];
    });
    return el('input', a);
  }

  function checkbox(checked) {
    return input('checkbox', '', { checked: !!checked });
  }

  function select(options, value) {
    var node = el('select');
    options.forEach(function (opt) {
      var o = el('option', { value: opt.value }, [opt.label]);
      if (String(opt.value) === String(value)) o.selected = true;
      node.appendChild(o);
    });
    return node;
  }

  function section(title, children) {
    return el('div', { class: 'section' }, [
      el('h2', { class: 'section-title' }, [title]),
      el('div', { class: 'section-body' }, children)
    ]);
  }

  // --- overview -----------------------------------------------------------

  function renderOverview(root) {
    var stateBox = el('div', { class: 'stat-grid' });

    function card(title, valueNode, sub) {
      return el('div', { class: 'stat' }, [
        el('div', { class: 'stat-title' }, [title]),
        el('div', { class: 'stat-value' }, [valueNode]),
        sub ? el('div', { class: 'stat-sub' }, [sub]) : null
      ]);
    }

    function refresh() {
      Promise.all([api.status(), api.version()]).then(function (results) {
        var st = results[0];
        var ver = results[1];
        var sup = st.supervisor || {};
        var running = sup.state === 'running';

        stateBox.innerHTML = '';
        stateBox.appendChild(
          card(
            '运行状态',
            el('span', { class: 'badge ' + (running ? 'badge-ok' : 'badge-idle') }, [running ? '运行中' : '已停止']),
            running ? '启动于 ' + fmtTime(sup.started_at) : '登录循环未运行'
          )
        );
        stateBox.appendChild(
          card(
            '上次执行',
            el('span', { class: 'stat-strong' }, [fmtTime(sup.last_run_at)]),
            '成功 ' + (sup.run_count - sup.fail_count) + ' 次 / 共 ' + sup.run_count + ' 次'
          )
        );
        stateBox.appendChild(
          card(
            '下次执行',
            el('span', { class: 'stat-strong' }, [fmtTime(sup.next_run_at)]),
            running && sup.next_run_at ? '约 ' + fmtDuration(sup.interval_seconds) + ' 后' : '—'
          )
        );
        stateBox.appendChild(
          card('守护模式', st.guardian_enabled ? '已启用' : '已关闭', '间隔 ' + fmtDuration(st.interval_seconds))
        );

        var account = $('#ov-account');
        if (account) {
          account.innerHTML = '';
          account.appendChild(
            el('div', { class: 'kv-list' }, [
              kv('认证地址', st.form && st.form.domain),
              kv('上网账号', st.form && st.form.username),
              kv('运营商', st.form && st.form.user_type),
              kv('密码', st.form && st.form.has_password ? '已设置' : '未设置'),
              kv('DDNS', st.ddns && st.ddns.enable ? st.ddns.provider + ' · ' + st.ddns.domain : '未启用'),
              kv('多网卡', st.interfaces || '关闭')
            ])
          );
        }

        var errBox = $('#ov-error');
        if (errBox) {
          errBox.innerHTML = '';
          if (sup.last_error) {
            errBox.appendChild(el('div', { class: 'alert alert-error' }, ['最近一次错误：' + sup.last_error]));
          }
        }

        var verBox = $('#ov-version');
        if (verBox) verBox.textContent = (ver.version || 'dev') + ' · ' + ver.go_version;

        // Keep the action buttons in sync with the loop state.
        var startBtn = $('#btn-start');
        var stopBtn = $('#btn-stop');
        if (startBtn) startBtn.disabled = running;
        if (stopBtn) stopBtn.disabled = !running;
      }).catch(function (err) {
        notify('读取状态失败：' + err.message, 'error');
      });
    }

    root.appendChild(
      el('div', { class: 'page-head' }, [
        el('h1', {}, ['概览']),
        el('div', { class: 'page-actions' }, [
          el('button', { class: 'btn', id: 'btn-refresh', onclick: refresh }, ['刷新']),
          el('button', { class: 'btn btn-primary', id: 'btn-login', onclick: function () {
            this.disabled = true;
            var btn = this;
            api.loginNow().then(function (res) {
              notify(res.ok ? '登录成功' : '登录失败：' + res.message, res.ok ? 'ok' : 'error');
              refresh();
            }).catch(function (err) {
              notify('登录请求失败：' + err.message, 'error');
            }).then(function () {
              btn.disabled = false;
            });
          } }, ['立即登录'])
        ])
      ])
    );

    root.appendChild(stateBox);
    root.appendChild(el('div', { id: 'ov-error' }));
    root.appendChild(section('当前账号', [el('div', { id: 'ov-account' })]));
    root.appendChild(
      section('服务控制', [
        el('div', { class: 'btn-row' }, [
          el('button', { class: 'btn btn-primary', id: 'btn-start', onclick: function () {
            api.service('start').then(function (res) {
              notify('已启动（' + res.message + '）', 'ok');
              refresh();
            }).catch(function (err) {
              notify('启动失败：' + err.message, 'error');
            });
          } }, ['启动']),
          el('button', { class: 'btn', id: 'btn-stop', onclick: function () {
            api.service('stop').then(function (res) {
              notify('已停止（' + res.message + '）', 'ok');
              refresh();
            }).catch(function (err) {
              notify('停止失败：' + err.message, 'error');
            });
          } }, ['停止']),
          el('button', { class: 'btn', id: 'btn-restart', onclick: function () {
            api.service('restart').then(function (res) {
              notify('已重启（' + res.message + '）', 'ok');
              refresh();
            }).catch(function (err) {
              notify('重启失败：' + err.message, 'error');
            });
          } }, ['重启'])
        ]),
        el('div', { class: 'help' }, ['运行的进程版本：', el('span', { id: 'ov-version' }, ['—'])])
      ])
    );

    refresh();
    return refresh;
  }

  function kv(key, value) {
    return el('div', { class: 'kv' }, [
      el('span', { class: 'kv-key' }, [key]),
      el('span', { class: 'kv-value' }, [value === undefined || value === null || value === '' ? '—' : String(value)])
    ]);
  }

  // --- settings -----------------------------------------------------------

  function renderSettings(root) {
    root.appendChild(el('div', { class: 'page-head' }, [el('h1', {}, ['基础设置'])]));
    var form = el('form', { class: 'form', onsubmit: function (e) { e.preventDefault(); } });
    root.appendChild(form);

    api.config().then(function (conf) {
      var f = conf.form || {};
      var s = conf.settings || {};
      var basic = s.basic || {};
      var guardian = s.guardian || {};
      var logConf = s.log || {};

      var domainInput = input('text', f.domain, { placeholder: 'www.msftconnecttest.com' });
      var userInput = input('text', f.username, { placeholder: '学号 / 手机号' });
      var typeInput = select(
        [
          { value: 'cmcc', label: '中国移动 cmcc' },
          { value: 'telecom', label: '中国电信 telecom' },
          { value: 'unicom', label: '中国联通 unicom' }
        ],
        f.user_type
      );
      var passInput = input('password', '', {
        placeholder: f.has_password ? '已设置，留空则不修改' : '请输入密码',
        autocomplete: 'new-password'
      });

      form.appendChild(
        section('登录账号', [
          field('认证地址', domainInput, '深澜认证页面的域名，通常无需修改'),
          field('上网账号', userInput),
          field('运营商', typeInput),
          field('密码', passInput, f.has_password ? '已保存密码，留空表示保持不变' : '尚未设置密码')
        ])
      );

      var timeoutInput = input('number', basic.timeout, { min: '1', max: '120' });
      var interfacesInput = input('text', basic.interfaces, { placeholder: '如 eth0|eth1，留空为单网卡' });
      var intervalInput = input('number', basic.interfaces_interval, { min: '0' });
      var dnsserverInput = input('text', basic.dns_server, { placeholder: '如 223.5.5.5:53' });
      var httpsBox = checkbox(basic.https);
      var skipVerifyBox = checkbox(basic.skip_cert_verify);

      form.appendChild(
        section('网络', [
          field('请求超时（秒）', timeoutInput),
          field('使用 HTTPS', httpsBox, '部分学校认证页仅支持 HTTP，不确定时请保持关闭'),
          field('跳过证书校验', skipVerifyBox),
          field('多网卡正则', interfacesInput, '填写后启用多网卡模式，值为网卡名正则'),
          field('多网卡检测间隔（秒）', intervalInput),
          field('自定义 DNS', dnsserverInput)
        ])
      );

      var guardianBox = checkbox(guardian.enable);
      var durationInput = input('number', guardian.duration || 300, { min: '10' });
      var debugBox = checkbox(logConf.debug_level);
      var writeBox = checkbox(logConf.write_file);

      form.appendChild(
        section('守护与日志', [
          field('启用守护模式', guardianBox, '关闭时进程只执行一次登录，建议开启'),
          field('检测间隔（秒）', durationInput, '两次登录检测之间的间隔'),
          field('调试日志', debugBox),
          field('写入日志文件', writeBox)
        ])
      );

      var logPathInput = input('text', logConf.log_path, { placeholder: './' });
      var logNameInput = input('text', logConf.log_name, { placeholder: '留空自动按时间命名' });
      form.appendChild(
        section('日志文件', [
          field('日志目录', logPathInput),
          field('日志文件名', logNameInput)
        ])
      );

      var saveBtn = el('button', { class: 'btn btn-primary', type: 'submit' }, ['保存设置']);
      saveBtn.addEventListener('click', function () {
        var patch = {
          form: {
            domain: domainInput.value,
            username: userInput.value,
            user_type: typeInput.value
          },
          settings: {
            basic: {
              https: httpsBox.checked,
              skip_cert_verify: skipVerifyBox.checked,
              timeout: Number(timeoutInput.value) || 5,
              interfaces: interfacesInput.value,
              interfaces_interval: Number(intervalInput.value) || 0,
              dns_server: dnsserverInput.value
            },
            guardian: {
              enable: guardianBox.checked,
              duration: Number(durationInput.value) || 300
            },
            log: {
              debug_level: debugBox.checked,
              write_file: writeBox.checked,
              log_path: logPathInput.value,
              log_name: logNameInput.value
            }
          }
        };
        if (passInput.value !== '') patch.form.password = passInput.value;

        saveBtn.disabled = true;
        api.saveConfig(patch).then(function () {
          notify('设置已保存', 'ok');
          passInput.value = '';
          passInput.placeholder = '已设置，留空则不修改';
        }).catch(function (err) {
          notify('保存失败：' + err.message, 'error');
        }).then(function () {
          saveBtn.disabled = false;
        });
      });

      form.appendChild(section('保存', [el('div', { class: 'btn-row' }, [saveBtn])]));
    }).catch(function (err) {
      notify('读取配置失败：' + err.message, 'error');
    });
  }

  // --- ddns ---------------------------------------------------------------

  function renderDdns(root) {
    root.appendChild(el('div', { class: 'page-head' }, [el('h1', {}, ['DDNS'])]));
    var form = el('form', { class: 'form', onsubmit: function (e) { e.preventDefault(); } });
    root.appendChild(form);

    api.config().then(function (conf) {
      var d = (conf.settings && conf.settings.ddns) || {};
      var dc = d.config || {};

      var enableBox = checkbox(d.enable);
      var providerSelect = select(
        [
          { value: 'cloudflare', label: 'Cloudflare' },
          { value: 'aliyun', label: '阿里云' },
          { value: 'dnspod', label: 'DNSPod' }
        ],
        d.provider || 'cloudflare'
      );
      var domainInput = input('text', d.domain, { placeholder: 'home.example.com' });
      var ttlInput = input('number', d.ttl || 600, { min: '60' });

      form.appendChild(
        section('基本', [
          field('启用 DDNS', enableBox, '登录成功后把当前 IP 写入域名解析'),
          field('解析服务商', providerSelect),
          field('域名', domainInput),
          field('TTL（秒）', ttlInput)
        ])
      );

      var providers = el('div', { class: 'providers' });

      // Cloudflare
      var cfZone = input('text', dc.cloudflare_zone, { placeholder: 'Zone ID' });
      var cfToken = input('password', '', {
        placeholder: dc.has_cloudflare_token ? '已设置，留空则不修改' : 'API Token'
      });
      providers.appendChild(
        section('Cloudflare', [field('Zone ID', cfZone), field('API Token', cfToken)])
      );

      // Aliyun
      var aliId = input('text', dc.aliyun_access_key_id, { placeholder: 'AccessKey ID' });
      var aliSecret = input('password', '', {
        placeholder: dc.has_aliyun_access_secret ? '已设置，留空则不修改' : 'AccessKey Secret'
      });
      providers.appendChild(
        section('阿里云', [field('AccessKey ID', aliId), field('AccessKey Secret', aliSecret)])
      );

      // DNSPod
      var dpId = input('text', dc.dnspod_secret_id, { placeholder: 'SecretId' });
      var dpKey = input('password', '', {
        placeholder: dc.has_dnspod_secret_key ? '已设置，留空则不修改' : 'SecretKey'
      });
      providers.appendChild(
        section('DNSPod', [field('SecretId', dpId), field('SecretKey', dpKey)])
      );

      form.appendChild(providers);

      var saveBtn = el('button', { class: 'btn btn-primary', type: 'submit' }, ['保存 DDNS']);
      saveBtn.addEventListener('click', function () {
        var providerPatch = {};
        if (cfZone.value !== '') providerPatch.cloudflare_zone = cfZone.value;
        if (cfToken.value !== '') providerPatch.cloudflare_token = cfToken.value;
        if (aliId.value !== '') providerPatch.aliyun_access_key_id = aliId.value;
        if (aliSecret.value !== '') providerPatch.aliyun_access_key_secret = aliSecret.value;
        if (dpId.value !== '') providerPatch.dnspod_secret_id = dpId.value;
        if (dpKey.value !== '') providerPatch.dnspod_secret_key = dpKey.value;

        var patch = {
          settings: {
            ddns: {
              enable: enableBox.checked,
              provider: providerSelect.value,
              domain: domainInput.value,
              ttl: Number(ttlInput.value) || 600,
              config: providerPatch
            }
          }
        };
        saveBtn.disabled = true;
        api.saveConfig(patch).then(function () {
          notify('DDNS 设置已保存', 'ok');
          cfToken.value = '';
          aliSecret.value = '';
          dpKey.value = '';
        }).catch(function (err) {
          notify('保存失败：' + err.message, 'error');
        }).then(function () {
          saveBtn.disabled = false;
        });
      });

      form.appendChild(section('保存', [el('div', { class: 'btn-row' }, [saveBtn])]));
    }).catch(function (err) {
      notify('读取配置失败：' + err.message, 'error');
    });
  }

  // --- advanced -----------------------------------------------------------

  function renderAdvanced(root) {
    root.appendChild(el('div', { class: 'page-head' }, [el('h1', {}, ['高级设置'])]));
    var form = el('form', { class: 'form', onsubmit: function (e) { e.preventDefault(); } });
    root.appendChild(form);

    api.config().then(function (conf) {
      var meta = conf.meta || {};
      var s = conf.settings || {};
      var reality = s.reality || {};
      var headers = s.custom_header || {};

      var acidInput = input('text', meta.acid, { placeholder: '5' });
      var encInput = input('text', meta.enc, { placeholder: 'srun_bx1' });
      var osInput = input('text', meta.os, { placeholder: 'Windows 10' });

      form.appendChild(
        section('认证参数', [
          field('acid', acidInput, '不确定时保持默认，或使用 -auto-acid 自动探测'),
          field('enc', encInput, '不确定时保持默认，或使用 -auto-enc 自动探测'),
          field('操作系统标识', osInput)
        ])
      );

      var uaInput = input('text', headers['User-Agent'] || '');
      form.appendChild(section('自定义请求头', [field('User-Agent', uaInput)]));

      var realityBox = checkbox(reality.enable);
      var realityAddr = input('text', reality.addr, { placeholder: 'http://www.baidu.com' });
      form.appendChild(
        section('连通性检测', [
          field('启用真实地址检测', realityBox),
          field('检测地址', realityAddr)
        ])
      );

      var backupBtn = el('button', { class: 'btn', type: 'button' }, ['导出配置']);
      backupBtn.addEventListener('click', function () {
        api.config().then(function (data) {
          var blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
          var url = URL.createObjectURL(blob);
          var a = el('a', { href: url, download: 'bitsrun-config.json' });
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          URL.revokeObjectURL(url);
          // The exported file is the redacted view: it has no secrets, which
          // is the point - it is for migration and bug reports, not backup.
          notify('已导出（不含密码等敏感信息）', 'ok');
        });
      });

      var saveBtn = el('button', { class: 'btn btn-primary', type: 'submit' }, ['保存']);
      saveBtn.addEventListener('click', function () {
        var customHeader = {};
        Object.keys(headers).forEach(function (k) {
          customHeader[k] = headers[k];
        });
        if (uaInput.value) customHeader['User-Agent'] = uaInput.value;

        var metaPatch = {
          n: meta.n,
          type: meta.type,
          acid: acidInput.value,
          enc: encInput.value,
          os: osInput.value,
          name: meta.name,
          info_prefix: meta.info_prefix,
          double_stack: meta.double_stack
        };

        saveBtn.disabled = true;
        api.saveConfig({
          meta: metaPatch,
          settings: {
            reality: { enable: realityBox.checked, addr: realityAddr.value },
            custom_header: customHeader
          }
        }).then(function () {
          notify('高级设置已保存', 'ok');
        }).catch(function (err) {
          notify('保存失败：' + err.message, 'error');
        }).then(function () {
          saveBtn.disabled = false;
        });
      });

      form.appendChild(section('其他', [el('div', { class: 'btn-row' }, [saveBtn, backupBtn])]));
    }).catch(function (err) {
      notify('读取配置失败：' + err.message, 'error');
    });
  }

  // --- logs ---------------------------------------------------------------

  function renderLogs(root) {
    root.appendChild(
      el('div', { class: 'page-head' }, [
        el('h1', {}, ['日志']),
        el('div', { class: 'page-actions' }, [
          el('button', { class: 'btn', id: 'log-toggle', onclick: function () {
            autoRefresh = !autoRefresh;
            this.textContent = autoRefresh ? '暂停刷新' : '继续刷新';
          } }, ['暂停刷新']),
          el('button', { class: 'btn', onclick: function () {
            $('#log-view').innerHTML = '';
            lastSeq = 0;
            pull();
          } }, ['清空显示'])
        ])
      ])
    );

    var view = el('pre', { class: 'log-view', id: 'log-view' });
    root.appendChild(el('div', { class: 'section' }, [el('div', { class: 'section-body' }, [view])]));

    var lastSeq = 0;
    var autoRefresh = true;
    var timer = null;

    function append(entries) {
      if (!entries.length) return;
      var stick = view.scrollTop + view.clientHeight >= view.scrollHeight - 40;
      entries.forEach(function (e) {
        var line = el('span', { class: 'log-line log-' + e.level }, [
          el('span', { class: 'log-time' }, [e.time + ' ']),
          el('span', { class: 'log-level' }, [e.level.toUpperCase() + ' ']),
          el('span', { class: 'log-msg' }, [e.message]),
          '\n'
        ]);
        view.appendChild(line);
      });
      if (stick) view.scrollTop = view.scrollHeight;
    }

    function pull() {
      api.logs(lastSeq, 200).then(function (res) {
        append(res.entries || []);
        if (res.last_seq) lastSeq = res.last_seq;
      }).catch(function (err) {
        notify('读取日志失败：' + err.message, 'error');
      });
    }

    pull();
    timer = setInterval(function () {
      if (autoRefresh && document.visibilityState === 'visible') pull();
    }, 2000);

    // Return a cleanup so leaving the page stops the poll.
    return function () {
      if (timer) clearInterval(timer);
    };
  }

  // --- router -------------------------------------------------------------

  var pages = [
    { id: 'overview', title: '概览', render: renderOverview },
    { id: 'settings', title: '基础设置', render: renderSettings },
    { id: 'ddns', title: 'DDNS', render: renderDdns },
    { id: 'advanced', title: '高级设置', render: renderAdvanced },
    { id: 'logs', title: '日志', render: renderLogs }
  ];

  var cleanup = null;

  function currentPage() {
    var id = (location.hash || '#/overview').replace(/^#\/?/, '') || 'overview';
    for (var i = 0; i < pages.length; i++) {
      if (pages[i].id === id) return pages[i];
    }
    return pages[0];
  }

  function render() {
    if (typeof cleanup === 'function') {
      cleanup();
      cleanup = null;
    }
    var page = currentPage();

    $$('#nav a').forEach(function (a) {
      a.classList.toggle('active', a.getAttribute('data-page') === page.id);
    });

    var root = $('#content');
    root.innerHTML = '';
    cleanup = page.render(root) || null;
    document.title = page.title + ' · BitSrunLoginGo';
  }

  function boot() {
    var nav = $('#nav');
    pages.forEach(function (p) {
      nav.appendChild(
        el('a', { href: '#/' + p.id, 'data-page': p.id }, [p.title])
      );
    });

    // Theme: follow the system preference, allow a manual override stored in
    // localStorage, exactly like LuCI's dark mode toggle.
    var themeBtn = $('#theme');
    var stored = null;
    try {
      stored = localStorage.getItem('theme');
    } catch (e) {
      stored = null;
    }
    if (stored) document.documentElement.setAttribute('data-theme', stored);

    if (themeBtn) {
      themeBtn.addEventListener('click', function () {
        var now = document.documentElement.getAttribute('data-theme');
        var next = now === 'dark' ? 'light' : now === 'light' ? '' : 'dark';
        if (next) {
          document.documentElement.setAttribute('data-theme', next);
        } else {
          document.documentElement.removeAttribute('data-theme');
        }
        try {
          if (next) localStorage.setItem('theme', next);
          else localStorage.removeItem('theme');
        } catch (e) {
          /* private mode: the toggle still works for this session */
        }
      });
    }

    window.addEventListener('hashchange', render);
    render();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
