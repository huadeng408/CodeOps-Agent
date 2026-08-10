# DISPUTED qrels — blind arbitration worksheet

Rows: **157**. Deterministic order (seed 20260810).

The prior AI verdicts, the dispute reason, the confidence and the gold relevance are all withheld on purpose: seeing them would anchor the arbitration on the earlier disagreement instead of on the document.

For each row decide **is this document relevant to this query** and fill in `verdict_relevant` (`yes` / `no`). The other verdict fields are optional. Judge at document level: the claimed section path is unreliable (178 of 180 do not exist in the index for the document they name).

---

## 1. `631ed44822cd23f9`  (go)

**Query** (en / command): Which special instructions such as NOP and UNDEF are available in Go assembly?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", 'Special instructions']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 2. `2c9ea2bf5427f2ca`  (kubernetes)

**Query** (en / code_api): How do you assign a Pod to a specific node using nodeSelector?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/configure-pod-container/assign-pods-nodes.md`

**Claimed section** (unverified): `['Assign Pods to Nodes']`

**Actual sections in the index:**

- `[]`
  > This page shows how to assign a Kubernetes Pod to a particular node in a  Kubernetes cluster.
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}} {{< version-check >}}
- `['{{% heading "prerequisites" %}}', 'Add a label to a node']`
  > Add a label to a node  1. List the {{< glossary_tooltip term_id="node" text="nodes" >}} in your cluster, along with their labels:  kubectl get nodes --show-labels  The output is similar to this:  NAME      STATUS    ROLES    AGE     VERSION        LABELS  worker0   Ready     <none>   1d      v1.13.0        ...,kubernetes.io/hostname=worker0  worker1   Ready     <none>   1d      v1.13.0        ...,kubernetes.io/hostname=worker1  worker2   Ready     <none>   1d      v1.13.0        ...,kubernetes.io/hostname=worker2  1. Choose one of your nodes, and add a label to it:  kubectl label nodes <your-n…
- `['{{% heading "prerequisites" %}}', 'Create a pod that gets scheduled to your chosen node']`
  > Create a pod that gets scheduled to your chosen node  This pod configuration file describes a pod that has a node selector,  `disktype: ssd`. This means that the pod will get scheduled on a node that has  a `disktype=ssd` label.  {{% code_sample file="pods/pod-nginx.yaml" %}}  1. Use the configuration file to create a pod that will get scheduled on your  chosen node:  kubectl apply -f https://k8s.io/examples/pods/pod-nginx.yaml  1. Verify that the pod is running on your chosen node:  kubectl get pods --output=wide  The output is similar to this:  NAME     READY     STATUS    RESTARTS   AGE…
- `['{{% heading "prerequisites" %}}', 'Create a pod that gets scheduled to specific node']`
  > Create a pod that gets scheduled to specific node  You can also schedule a pod to one specific node via setting `nodeName`.  {{% code_sample file="pods/pod-nginx-specific-node.yaml" %}}  Use the configuration file to create a pod that will get scheduled on `foo-node` only.
- `['{{% heading "prerequisites" %}}', '{{% heading "whatsnext" %}}']`
  > {{% heading "whatsnext" %}}  * Learn more about [labels and selectors](/docs/concepts/overview/working-with-objects/labels/).  * Learn more about [nodes](/docs/concepts/architecture/nodes/).

`verdict_relevant:` ______   `notes:` ______

---

## 3. `8397961f981ca089`  (postgresql)

**Query** (zh / config): 怎么配置 PostgreSQL 的日志记录和错误报告？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/config.sgml`

**Claimed section** (unverified): `['Server Configuration', 'Error Reporting and Logging']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/config.sgml -->  <chapter id="runtime-config">  <title>Server Configuration</title>  <indexterm>  <primary>configuration</primary>  <secondary>of the server</secondary>  </indexterm>  <para>  There are many configuration parameters that affect the behavior of  the database system. In the first section of this chapter we  describe how to interact with configuration parameters. The subsequent sections  discuss each parameter in detail.  </para>  <sect1 id="config-setting">  <title>Setting Parameters</title>  <indexterm><primary>GUC</primary></indexterm>  <sect2 id="config-setti…
- `['This is a comment']`
  > This is a comment  log_connections = all  log_destination = 'syslog'  search_path = '"$user", public'  shared_buffers = 128MB  </programlisting>  One parameter is specified per line. The equal sign between name and  value is optional. Whitespace is insignificant (except within a quoted  parameter value) and blank lines are  ignored. Hash marks (<literal>#</literal>) designate the remainder  of the line as a comment. Parameter values that are not simple  identifiers or numbers must be single-quoted. To embed a single  quote in a parameter value, write either two quotes (preferred)  or backslash…
- `['This is a comment']`
  > Other clients and libraries might provide their own mechanisms,  via the shell or otherwise, that allow the user to alter session  settings without direct use of SQL commands.  </para>  </listitem>  </itemizedlist>  </sect2>  <sect2 id="config-includes">  <title>Managing Configuration File Contents</title>  <para>  <productname>PostgreSQL</productname> provides several features for breaking  down complex <filename>postgresql.conf</filename> files into sub-files.  These features are especially useful when managing multiple servers  with related, but not identical, configurations.  </para>  <par…
- `['This is a comment']`
  > data directory is specified by the <option>-D</option> command-line  option or the <envar>PGDATA</envar> environment variable, and the  configuration files are all found within the data directory.  </para>  <para>  If you wish to keep the configuration files elsewhere than the  data directory, the <command>postgres</command> <option>-D</option>  command-line option or <envar>PGDATA</envar> environment variable  must point to the directory containing the configuration files,  and the <varname>data_directory</varname> parameter must be set in  <filename>postgresql.conf</filename> (or on the comm…
- `['This is a comment']`
  > created by default.  This parameter can only be set at server start.  </para>  <para>  In addition to the socket file itself, which is named  <literal>.s.PGSQL.<replaceable>nnnn</replaceable></literal> where  <replaceable>nnnn</replaceable> is the server's port number, an ordinary file  named <literal>.s.PGSQL.<replaceable>nnnn</replaceable>.lock</literal> will be  created in each of the <varname>unix_socket_directories</varname> directories.  Neither file should ever be removed manually.  For sockets in the abstract namespace, no lock file is created.  </para>  </listitem>  </varlistentry>  <…
- `['This is a comment']`
  > This option relies on kernel events exposed by Linux, macOS, illumos  and the BSD family of operating systems, and is not currently available  on other systems.  </para>  <para>  If the value is specified without units, it is taken as milliseconds.  The default value is <literal>0</literal>, which disables connection  checks. Without connection checks, the server will detect the loss of  the connection only at the next interaction with the socket, when it  waits for, receives or sends data.  </para>  <para>  For the kernel itself to detect lost TCP connections reliably and within  a known time…

`verdict_relevant:` ______   `notes:` ______

---

## 4. `fae34fdddc267915`  (python)

**Query** (en / concept): What is the difference between mutable and immutable built-in types in Python?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/stdtypes.rst`

**Claimed section** (unverified): `['Built-in Types']`

**Actual sections in the index:**

- `[]`
  > .. XXX: reference/datamodel and this have quite a few overlaps!  .. _bltin-types:  **************  Built-in Types  **************  The following sections describe the standard types that are built into the  interpreter.  .. index:: pair: built-in; types  The principal built-in types are numerics, sequences, mappings, classes,  instances and exceptions.  Some collection classes are mutable. The methods that add, subtract, or  rearrange their members in place, and don't return a specific item, never return  the collection instance itself but ``None``.  Some operations are supported by several ob…
- `[]`
  > in :data:`sys.float_info`. Complex numbers have a real and imaginary  part, which are each a floating-point number. To extract these parts  from a complex number *z*, use ``z.real`` and ``z.imag``. (The standard  library includes the additional numeric types :mod:`fractions.Fraction`, for  rationals, and :mod:`decimal.Decimal`, for floating-point numbers with  user-definable precision.)  .. index::  pair: numeric; literals  pair: integer; literals  pair: floating-point; literals  pair: complex number; literals  pair: hexadecimal; literals  pair: octal; literals  pair: binary; literals  Numbers…
- `[]`
  > pair: operator; ~ (tilde)  Bitwise operations only make sense for integers. The result of bitwise  operations is calculated as though carried out in two's complement with an  infinite number of sign bits.  The priorities of the binary bitwise operations are all lower than the numeric  operations and higher than the comparisons; the unary operation ``~`` has the  same priority as the other unary numeric operations (``+`` and ``-``).  This table lists the bitwise operations sorted in ascending priority:  +------------+--------------------------------+----------+  | Operation | Result | Notes |…
- `[]`
  > If the argument is an integer or a floating-point number, a  floating-point number with the same value (within Python's floating-point  precision) is returned. If the argument is outside the range of a Python  float, an :exc:`OverflowError` will be raised.  For a general Python object ``x``, ``float.from_number(x)`` delegates to  ``x.__float__()``.  If :meth:`~object.__float__` is not defined then it falls back  to :meth:`~object.__index__`.  .. versionadded:: 3.14  .. method:: float.as_integer_ratio()  Return a pair of integers whose ratio is exactly equal to the  original float. The ratio is…
- `['Remove common factors of P. (Unnecessary if m and n already coprime.)']`
  > Remove common factors of P. (Unnecessary if m and n already coprime.)  while m % P == n % P == 0:  m, n = m // P, n // P  if n % P == 0:  hash_value = sys.hash_info.inf  else:
- `["Fermat's Little Theorem: pow(n, P-1, P) is 1, so"]`
  > Fermat's Little Theorem: pow(n, P-1, P) is 1, so

`verdict_relevant:` ______   `notes:` ______

---

## 5. `0e11ee60c23fcba5`  (kubernetes)

**Query** (en / config): How do you set kubelet parameters using a configuration file?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/administer-cluster/kubelet-config-file.md`

**Claimed section** (unverified): `['Set Kubelet Parameters Via A Configuration File']`

**Actual sections in the index:**

- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  Some steps in this page use the `jq` tool. If you don't have `jq`, you can  install it via your operating system's software sources, or fetch it from  [https://jqlang.github.io/jq/](https://jqlang.github.io/jq/).  Some steps also involve installing `curl`, which can be installed via your  operating system's software sources.  A subset of the kubelet's configuration parameters may be  set via an on-disk config file, as a substitute for command-line flags.  Providing parameters via a config file is the recommended approach because  it simplifies node deployment a…
- `['{{% heading "prerequisites" %}}', 'Create the config file']`
  > Create the config file  The subset of the kubelet's configuration that can be configured via a file  is defined by the  [`KubeletConfiguration`](/docs/reference/config-api/kubelet-config.v1beta1/)  struct.  The configuration file must be a JSON or YAML representation of the parameters  in this struct. Make sure the kubelet has read permissions on the file.  Here is an example of what this file might look like:  apiVersion: kubelet.config.k8s.io/v1beta1  kind: KubeletConfiguration  address: "192.168.0.8"  port: 20250  serializeImagePulls: false  evictionHard:  memory.available:  "100Mi"  nodefs…
- `['{{% heading "prerequisites" %}}', 'Start a kubelet process configured via the config file']`
  > Start a kubelet process configured via the config file  {{< note >}}  If you use kubeadm to initialize your cluster, use the kubelet-config while creating your cluster with `kubeadm init`.  See [configuring kubelet using kubeadm](/docs/setup/production-environment/tools/kubeadm/kubelet-integration/) for details.  {{< /note >}}  Start the kubelet with the `--config` flag set to the path of the kubelet's config file.  The kubelet will then load its config from this file.  Note that command line flags which target the same value as a config file will override that value.  This helps ensure backwa…
- `['{{% heading "prerequisites" %}}', 'Drop-in directory for kubelet configuration files {#kubelet-conf-d}']`
  > Drop-in directory for kubelet configuration files {#kubelet-conf-d}  You can specify a drop-in configuration directory for the kubelet. By default, the kubelet does not look  for drop-in configuration files anywhere - you must specify a path.  For example: `--config-dir=/etc/kubernetes/kubelet.conf.d`  For Kubernetes v1.28 to v1.29, you can only specify `--config-dir` if you also set  the environment variable `KUBELET_CONFIG_DROPIN_DIR_ALPHA` for the kubelet process (the value  of that variable does not matter).  {{< note >}}  The suffix of a valid kubelet drop-in configuration file **must** b…
- `['{{% heading "prerequisites" %}}', 'Drop-in directory for kubelet configuration files {#kubelet-conf-d}', 'Kubelet configuration merging order']`
  > Kubelet configuration merging order  On startup, the kubelet merges configuration from:  * Feature gates specified over the command line (lowest precedence).  * The kubelet configuration.  * Drop-in configuration files, according to sort order.  * Command line arguments excluding feature gates (highest precedence).  {{< note >}}  The config drop-in dir mechanism for the kubelet is similar but different from how the `kubeadm` tool allows you to patch configuration.  The `kubeadm` tool uses a specific [patching strategy](/docs/setup/production-environment/tools/kubeadm/control-plane-flags/#patch…
- `['{{% heading "prerequisites" %}}', 'Viewing the kubelet configuration']`
  > Viewing the kubelet configuration  Since the configuration could now be spread over multiple files with this feature, if someone wants to inspect the final actuated configuration,  they can follow these steps to inspect the kubelet configuration:  1. Start a proxy server using [`kubectl proxy`](/docs/reference/kubectl/generated/kubectl_proxy/) in your terminal.  kubectl proxy  Which gives output like:  Starting to serve on 127.0.0.1:8001  1. Open another terminal window and use `curl` to fetch the kubelet configuration.  Replace `<node-name>` with the actual name of your node:  curl -X GET htt…

`verdict_relevant:` ______   `notes:` ______

---

## 6. `491ce20d563961db`  (postgresql)

**Query** (zh / command): 怎么用 pg_restore 恢复自定义格式的备份文件？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/dblink.sgml`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/dblink.sgml -->  <sect1 id="dblink" xreflabel="dblink">  <title>dblink &mdash; connect to other PostgreSQL databases</title>  <indexterm zone="dblink">  <primary>dblink</primary>  </indexterm>  <para>  <filename>dblink</filename> is a module that supports connections to  other <productname>PostgreSQL</productname> databases from within a database  session.  </para>  <para>  <filename>dblink</filename> can report the following wait events under the wait  event type <literal>Extension</literal>.  </para>  <variablelist>  <varlistentry>  <term><literal>DblinkConnect</literal></t…
- `[]`
  > it may be appropriate to grant <literal>EXECUTE</literal> permission for  <function>dblink_connect_u()</function> to specific users who are considered  trustworthy, but this should be done with care. It is also recommended  that any <filename>~/.pgpass</filename> file belonging to the server's user  <emphasis>not</emphasis> contain any records specifying a wildcard host name.  </para>  <para>  For further details see <function>dblink_connect()</function>.  </para>  </refsect1>  </refentry>  <refentry id="contrib-dblink-disconnect">  <indexterm>  <primary>dblink_disconnect</primary>  </indexter…
- `[]`
  > When two <type>text</type> arguments are given, the first one is first  looked up as a persistent connection's name; if found, the command  is executed on that connection. If not found, the first argument  is treated as a connection info string as for <function>dblink_connect</function>,  and the indicated connection is made just for the duration of this command.  </para>  </refsect1>  <refsect1>  <title>Arguments</title>  <variablelist>  <varlistentry>  <term><parameter>connname</parameter></term>  <listitem>  <para>  Name of the connection to use; omit this parameter to use the  unnamed conn…
- `[]`
  > funcname | source  ----------+--------  (0 rows)  </screen>  </refsect1>  </refentry>  <refentry id="contrib-dblink-close">  <indexterm>  <primary>dblink_close</primary>  </indexterm>  <refmeta>  <refentrytitle>dblink_close</refentrytitle>  <manvolnum>3</manvolnum>  </refmeta>  <refnamediv>  <refname>dblink_close</refname>  <refpurpose>closes a cursor in a remote database</refpurpose>  </refnamediv>  <refsynopsisdiv>  <synopsis>  dblink_close(text cursorname [, bool fail_on_error]) returns text  dblink_close(text connname, text cursorname [, bool fail_on_error]) returns text  </synopsis>  </re…
- `[]`
  > and the function returns no rows.  </para>  </listitem>  </varlistentry>  </variablelist>  </refsect1>  <refsect1>  <title>Return Value</title>  <para>  For an async query (that is, an SQL statement returning rows),  the function returns the row(s) produced by the query. To use this  function, you will need to specify the expected set of columns,  as previously discussed for <function>dblink</function>.  </para>  <para>  For an async command (that is, an SQL statement not returning rows),  the function returns a single row with a single text column containing  the command's status string. It i…
- `[]`
  > <term><parameter>num_primary_key_atts</parameter></term>  <listitem>  <para>  The number of primary key fields.  </para>  </listitem>  </varlistentry>  <varlistentry>  <term><parameter>src_pk_att_vals_array</parameter></term>  <listitem>  <para>  Values of the primary key fields to be used to look up the  local tuple. Each field is represented in text form.  An error is thrown if there is no local row with these  primary key values.  </para>  </listitem>  </varlistentry>  <varlistentry>  <term><parameter>tgt_pk_att_vals_array</parameter></term>  <listitem>  <para>  Values of the primary key fi…

`verdict_relevant:` ______   `notes:` ______

---

## 7. `189e91ef3462edfc`  (kubernetes)

**Query** (zh / troubleshooting): kubeadm 初始化集群失败，常见的错误怎么解决？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/setup/production-environment/tools/kubeadm/troubleshooting-kubeadm.md`

**Claimed section** (unverified): `['Troubleshooting kubeadm']`

**Actual sections in the index:**

- `[]`
  > As with any program, you might run into an error installing or running kubeadm.  This page lists some common failure scenarios and have provided steps that can help you understand and fix the problem.  If your problem is not listed below, please follow the following steps:  - If you think your problem is a bug with kubeadm:  - Go to [github.com/kubernetes/kubeadm](https://github.com/kubernetes/kubeadm/issues) and search for existing issues.  - If no issue exists, please [open one](https://github.com/kubernetes/kubeadm/issues/new) and follow the issue template.  - If you are unsure about how ku…
- `['Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC']`
  > Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC  In v1.18 kubeadm added prevention for joining a Node in the cluster if a Node with the same name already exists.  This required adding RBAC for the bootstrap-token user to be able to GET a Node object.  However this causes an issue where `kubeadm join` from v1.18 cannot join a cluster created by kubeadm v1.17.  To workaround the issue you have two options:  Execute `kubeadm init phase bootstrap-token` on a control-plane node using kubeadm v1.18.  Note that this enables the rest of the bootstrap-token permissions as well.…
- `['Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC', '`ebtables` or some similar executable not found during installation']`
  > `ebtables` or some similar executable not found during installation  If you see the following warnings while running `kubeadm init`  [preflight] WARNING: ebtables not found in system path  [preflight] WARNING: ethtool not found in system path  Then you may be missing `ebtables`, `ethtool` or a similar executable on your node.  You can install them with the following commands:  - For Ubuntu/Debian users, run `apt install ebtables ethtool`.  - For CentOS/Fedora users, run `yum install ebtables ethtool`.
- `['Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC', 'kubeadm blocks waiting for control plane during installation']`
  > kubeadm blocks waiting for control plane during installation  If you notice that `kubeadm init` hangs after printing out the following line:  [apiclient] Created API client, waiting for the control plane to become ready  This may be caused by a number of problems. The most common are:  - network connection problems. Check that your machine has full network connectivity before continuing.  - the cgroup driver of the container runtime differs from that of the kubelet. To understand how to  configure it properly, see [Configuring a cgroup driver](/docs/tasks/administer-cluster/kubeadm/configure-c…
- `['Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC', 'kubeadm blocks when removing managed containers']`
  > kubeadm blocks when removing managed containers  The following could happen if the container runtime halts and does not remove  any Kubernetes-managed containers:  sudo kubeadm reset  [preflight] Running pre-flight checks  [reset] Stopping the kubelet service  [reset] Unmounting mounted directories in "/var/lib/kubelet"  [reset] Removing kubernetes-managed containers  (block)  A possible solution is to restart the container runtime and then re-run `kubeadm reset`.  You can also use `crictl` to debug the state of the container runtime. See  [Debugging Kubernetes nodes with crictl](/docs/tasks/d…
- `['Not possible to join a v1.18 Node to a v1.17 cluster due to missing RBAC', 'Pods in `RunContainerError`, `CrashLoopBackOff` or `Error` state']`
  > Pods in `RunContainerError`, `CrashLoopBackOff` or `Error` state  Right after `kubeadm init` there should not be any pods in these states.  - If there are pods in one of these states _right after_ `kubeadm init`, please open an  issue in the kubeadm repo. `coredns` (or `kube-dns`) should be in the `Pending` state  until you have deployed the network add-on.  - If you see Pods in the `RunContainerError`, `CrashLoopBackOff` or `Error` state  after deploying the network add-on and nothing happens to `coredns` (or `kube-dns`),  it's very likely that the Pod Network add-on that you installed is som…

`verdict_relevant:` ______   `notes:` ______

---

## 8. `690a60eeabf5fd2f`  (go)

**Query** (en / concept): How do you declare type parameters in Go and what kinds of constraints can be placed on them?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Declarations and scope', 'Type parameter declarations']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 9. `b66e97689d51dfd4`  (kubernetes)

**Query** (en / command): How do you scale a Deployment with kubectl, and what does it change?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/run-application/scale-deployment.md`

**Claimed section** (unverified): `['Horizontal Manual Scaling for a Deployment']`

**Actual sections in the index:**

- `[]`
  > This page shows how to manually scale a Deployment horizontally, by changing its replica count.  Manual scaling lets you directly control the number of running Pods for predictable load changes or cost management.  This is different from _vertical scaling_: leaving the replica count the same, but adjusting  the amount of resources available to each Pod.
- `['{{% heading "objectives" %}}']`
  > {{% heading "objectives" %}}  - Scaling up a Deployment to handle more traffic.  - Scaling down a Deployment to conserve resources.  - Scaling a Deployment to zero to suspend a workload.  - Understanding when to use manual scaling versus a HorizontalPodAutoscaler.
- `['{{% heading "objectives" %}}', '{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}  You need an existing Deployment. If you do not have one, and you just want to practice,  you can create the nginx Deployment from  [Run a Stateless Application Using a Deployment](/docs/tasks/run-application/run-stateless-application-deployment/):  kubectl apply -f https://k8s.io/examples/application/deployment.yaml  Verify the Deployment runs two Pods:  kubectl get deployment nginx-deployment  The output is similar to:  NAME               READY   UP-TO-DATE   AVAILABLE   AGE  nginx-deployment   2/2     2            2…
- `['{{% heading "objectives" %}}', 'Scaling up a Deployment']`
  > Scaling up a Deployment  There are several different ways you can change the replica count for an  existing Deployment.
- `['{{% heading "objectives" %}}', 'Scaling up a Deployment', 'Scaling up using `kubectl scale`']`
  > Scaling up using `kubectl scale`  Use `kubectl scale` to set the replica count:  kubectl scale deployment/nginx-deployment --replicas=4  The output is similar to:  deployment.apps/nginx-deployment scaled  Verify that the Deployment has four Pods:  kubectl get deployment nginx-deployment  The output is similar to:  NAME               READY   UP-TO-DATE   AVAILABLE   AGE  nginx-deployment   4/4     4            4           1m
- `['{{% heading "objectives" %}}', 'Scaling up a Deployment', 'Declarative scaling using `kubectl apply`']`
  > Declarative scaling using `kubectl apply`  Instead of running an imperative command, you can update the manifest file and  apply it. This approach fits well with version-controlled configuration  workflows.  Save the current Deployment configuration to a local file:  kubectl get deployment nginx-deployment -o yaml > /tmp/nginx-deployment.yaml  Edit `/tmp/nginx-deployment.yaml` and change `.spec.replicas` to `4`.  Before applying, compare your local changes against the cluster state:  kubectl diff -f /tmp/nginx-deployment.yaml  Apply the edited manifest:  kubectl apply -f /tmp/nginx-deployment.…

`verdict_relevant:` ______   `notes:` ______

---

## 10. `51f7178572793c78`  (kubernetes)

**Query** (zh / troubleshooting): 怎么进入运行中的 Pod 容器里调试，查看日志和进程？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/debug/debug-application/debug-running-pod.md`

**Claimed section** (unverified): `['Debug Running Pods']`

**Actual sections in the index:**

- `[]`
  > This page explains how to debug Pods running (or crashing) on a Node.
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  * Your {{< glossary_tooltip text="Pod" term_id="pod" >}} should already be  scheduled and running. If your Pod is not yet running, start with [Debugging  Pods](/docs/tasks/debug/debug-application/).  * For some of the advanced debugging steps you need to know on which Node the  Pod is running and have shell access to run commands on that Node. You don't  need that access to run the standard debug steps that use `kubectl`.
- `['{{% heading "prerequisites" %}}', 'Using `kubectl describe pod` to fetch details about pods']`
  > Using `kubectl describe pod` to fetch details about pods  For this example we'll use a Deployment to create two pods, similar to the earlier example.  {{% code_sample file="application/nginx-with-request.yaml" %}}  Create deployment by running following command:  kubectl apply -f https://k8s.io/examples/application/nginx-with-request.yaml  deployment.apps/nginx-deployment created  Check pod status by following command:  kubectl get pods  NAME                                READY   STATUS    RESTARTS   AGE  nginx-deployment-67d4bdd6f5-cx2nz   1/1     Running   0          13s  nginx-deployment-6…
- `['{{% heading "prerequisites" %}}', 'Example: debugging Pending Pods']`
  > Example: debugging Pending Pods  A common scenario that you can detect using events is when you've created a Pod that won't fit on any node. For example, the Pod might request more resources than are free on any node, or it might specify a label selector that doesn't match any nodes. Let's say we created the previous Deployment with 5 replicas (instead of 2) and requesting 600 millicores instead of 500, on a four-node cluster where each (virtual) machine has 1 CPU. In that case one of the Pods will not be able to schedule. (Note that because of the cluster addon pods such as fluentd, skydns, e…
- `['{{% heading "prerequisites" %}}', 'Examining pod logs {#examine-pod-logs}']`
  > Examining pod logs {#examine-pod-logs}  First, look at the logs of the affected container:  kubectl logs ${POD_NAME} -c ${CONTAINER_NAME}  If your container has previously crashed, you can access the previous container's crash log with:  kubectl logs ${POD_NAME} -c ${CONTAINER_NAME} --previous
- `['{{% heading "prerequisites" %}}', 'Debugging with container exec {#container-exec}']`
  > Debugging with container exec {#container-exec}  If the {{< glossary_tooltip text="container image" term_id="image" >}} includes  debugging utilities, as is the case with images built from Linux and Windows OS  base images, you can run commands inside a specific container with  `kubectl exec`:  kubectl exec ${POD_NAME} -c ${CONTAINER_NAME} -- ${CMD} ${ARG1} ${ARG2} ... ${ARGN}  {{< note >}}  `-c ${CONTAINER_NAME}` is optional. You can omit it for Pods that only contain a single container.  {{< /note >}}  As an example, to look at the logs from a running Cassandra pod, you might run  kubectl ex…

`verdict_relevant:` ______   `notes:` ______

---

## 11. `280c9208f0834d24`  (git)

**Query** (zh / config): 怎么给 Git 配置命令别名（alias）来简化常用操作？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/config/alias.adoc`

**Claimed section** (unverified): `['alias.*']`

**Actual sections in the index:**

- `[]`
  > alias.*::  alias.*.command::  Command aliases for the linkgit:git[1] command wrapper. Aliases  can be defined using two syntaxes:  +  --  1. Without a subsection, e.g., `[alias] co = checkout`. The alias  name ("co" in this example) is  limited to ASCII alphanumeric characters and `-`,  and is matched case-insensitively.  2. With a subsection, e.g., `[alias "co"] command = checkout`. The  alias name can contain any characters (except for newlines and NUL bytes),  including UTF-8, and is matched case-sensitively as raw bytes.  You define the action of the alias in the `command`.  --  +  Example…
- `['Without subsection (ASCII alphanumeric and dash only)']`
  > Without subsection (ASCII alphanumeric and dash only)  [alias]  co = checkout  st = status
- `['With subsection (allows any characters, including UTF-8)']`
  > With subsection (allows any characters, including UTF-8)  [alias "hämta"]  command = fetch  [alias "rätta till"]  command = commit --amend  ----  +  With a Git alias defined, e.g.,  +  $ git config --global alias.last "cat-file commit HEAD"
- `['Which is equivalent to']`
  > Which is equivalent to  $ git config --global alias.last.command "cat-file commit HEAD"  +  `git last` is equivalent to `git cat-file commit HEAD`.  +  To avoid confusion and troubles with script usage, aliases that  hide existing Git commands are ignored except for deprecated  commands. Arguments are split by  spaces, the usual shell quoting and escaping are supported.  A quote pair or a backslash can be used to quote them.  +  Note that the first word of an alias does not necessarily have to be a  command. It can be a command-line option that will be passed into the  invocation of `git`. In…

`verdict_relevant:` ______   `notes:` ______

---

## 12. `b57ee4cd3b727402`  (python)

**Query** (en / troubleshooting): What happens when a Python program raises an exception, and how do try and except handle it?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/errors.rst`

**Claimed section** (unverified): `['Errors and Exceptions', 'Handling Exceptions']`

**Actual sections in the index:**

- `[]`
  > .. _tut-errors:  *********************  Errors and Exceptions  *********************  Until now error messages haven't been more than mentioned, but if you have tried  out the examples you have probably seen some. There are (at least) two  distinguishable kinds of errors: *syntax errors* and *exceptions*.  .. _tut-syntaxerrors:  Syntax Errors  =============  Syntax errors, also known as parsing errors, are perhaps the most common kind of  complaint you get while you are still learning Python::  >>> while True print('Hello world')  File "<stdin>", line 1  while True print('Hello world')  ^^^^^…
- `[]`
  > :exc:`BaseException` is the common base class of all exceptions. One of its  subclasses, :exc:`Exception`, is the base class of all the non-fatal exceptions.  Exceptions which are not subclasses of :exc:`Exception` are not typically  handled, because they are used to indicate that the program should terminate.  They include :exc:`SystemExit` which is raised by :meth:`sys.exit` and  :exc:`KeyboardInterrupt` which is raised when a user wishes to interrupt  the program.  :exc:`Exception` can be used as a wildcard that catches (almost) everything.  However, it is good practice to be as specific as…
- `['exc must be exception instance or None.']`
  > exc must be exception instance or None.  raise RuntimeError from exc  This can be useful when you are transforming exceptions. For example::  >>> def func():  ... raise ConnectionError  ...  >>> try:  ... func()  ... except ConnectionError as exc:  ... raise RuntimeError('Failed to open database') from exc  ...  Traceback (most recent call last):  File "<stdin>", line 2, in <module>  func()  ~~~~^^  File "<stdin>", line 2, in func  ConnectionError  <BLANKLINE>  The above exception was the direct cause of the following exception:  <BLANKLINE>  Traceback (most recent call last):  File "<stdin>",…
- `['exc must be exception instance or None.']`
  > ... raise ExceptionGroup('there were problems', excs)  ...  >>> f()  + Exception Group Traceback (most recent call last):  | File "<stdin>", line 1, in <module>  | f()  | ~^^  | File "<stdin>", line 3, in f  | raise ExceptionGroup('there were problems', excs)  | ExceptionGroup: there were problems (2 sub-exceptions)  +-+---------------- 1 ----------------  | OSError: error 1  +---------------- 2 ----------------  | SystemError: error 2  +------------------------------------  >>> try:  ... f()  ... except Exception as e:  ... print(f'caught {type(e)}: {e}')  ...  caught <class 'ExceptionGroup'>…

`verdict_relevant:` ______   `notes:` ______

---

## 13. `9e28180f4524531e`  (python)

**Query** (en / concept): What data structures does Python provide for sequences, and which one is best to use as a stack or a queue?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/datastructures.rst`

**Claimed section** (unverified): `['Data Structures']`

**Actual sections in the index:**

- `[]`
  > .. _tut-structures:  ***************  Data Structures  ***************  This chapter describes some things you've learned about already in more detail,  and adds some new things as well.  .. _tut-morelists:  More on Lists  =============  The :ref:`list <typesseq-list>` data type has some more methods. Here are all  of the methods of list objects:  .. method:: list.append(value, /)  :noindex:  Add an item to the end of the list. Similar to ``a[len(a):] = [x]``.  .. method:: list.extend(iterable, /)  :noindex:  Extend the list by appending all the items from the iterable. Similar to  ``a[len(a):…
- `[]`
  > [(1, 3), (1, 4), (2, 3), (2, 1), (2, 4), (3, 1), (3, 4)]  and it's equivalent to::  >>> combs = []  >>> for x in [1,2,3]:  ... for y in [3,1,4]:  ... if x != y:  ... combs.append((x, y))  ...  >>> combs  [(1, 3), (1, 4), (2, 3), (2, 1), (2, 4), (3, 1), (3, 4)]  Note how the order of the :keyword:`for` and :keyword:`if` statements is the  same in both these snippets.  If the expression is a tuple (e.g. the ``(x, y)`` in the previous example),  it must be parenthesized. ::  >>> vec = [-4, -2, 0, 2, 4]  >>> # create a new list with the values doubled  >>> [x*2 for x in vec]  [-8, -4, 0, 4, 8]  >>…
- `[]`
  > We saw that lists and strings have many common properties, such as indexing and  slicing operations. They are two examples of *sequence* data types (see  :ref:`typesseq`). Since Python is an evolving language, other sequence data  types may be added. There is also another standard sequence data type: the  *tuple*.  A tuple consists of a number of values separated by commas, for instance::  >>> t = 12345, 54321, 'hello!'  >>> t[0]  12345  >>> t  (12345, 54321, 'hello!')  >>> # Tuples may be nested:  >>> u = t, (1, 2, 3, 4, 5)  >>> u  ((12345, 54321, 'hello!'), (1, 2, 3, 4, 5))  >>> # Tuples are…
- `[]`
  > pair with ``del``. If you store using a key that is already in use, the old  value associated with that key is forgotten.  Extracting a value for a non-existent key by subscripting (``d[key]``) raises a  :exc:`KeyError`. To avoid getting this error when trying to access a possibly  non-existent key, use the :meth:`~dict.get` method instead, which returns  ``None`` (or a specified default value) if the key is not in the dictionary.  Performing ``list(d)`` on a dictionary returns a list of all the keys  used in the dictionary, in insertion order (if you want it sorted, just use  ``sorted(d)`` in…
- `[]`
  > >>> non_null = string1 or string2 or string3  >>> non_null  'Trondheim'  Note that in Python, unlike C, assignment inside expressions must be done  explicitly with the  :ref:`walrus operator <why-can-t-i-use-an-assignment-in-an-expression>` ``:=``.  This avoids a common class of problems encountered in C programs: typing ``=``  in an expression when ``==`` was intended.  .. _tut-comparing:  Comparing Sequences and Other Types  ===================================  Sequence objects typically may be compared to other objects with the same sequence  type. The comparison uses *lexicographical* orde…

`verdict_relevant:` ______   `notes:` ______

---

## 14. `08e003c54e942eb1`  (kubernetes)

**Query** (en / concept): What are the main components of a Kubernetes cluster and what does each one do?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/overview/components.md`

**Claimed section** (unverified): `['Kubernetes Components']`

**Actual sections in the index:**

- `[]`
  > This page provides a high-level overview of the essential components that make up a Kubernetes cluster.  {{< figure src="/images/docs/components-of-kubernetes.svg" alt="Components of Kubernetes" caption="The components of a Kubernetes cluster" class="diagram-large" clicktozoom="true" >}}
- `['Core Components']`
  > Core Components  A Kubernetes cluster consists of a control plane and one or more worker nodes.  Here's a brief overview of the main components:
- `['Core Components', 'Control Plane Components']`
  > Control Plane Components  Manage the overall state of the cluster:  [kube-apiserver](/docs/concepts/architecture/#kube-apiserver)  : The core component server that exposes the Kubernetes HTTP API.  [etcd](/docs/concepts/architecture/#etcd)  : Consistent and highly-available key value store for all API server data.  [kube-scheduler](/docs/concepts/architecture/#kube-scheduler)  : Looks for Pods not yet bound to a node, and assigns each Pod to a suitable node.  [kube-controller-manager](/docs/concepts/architecture/#kube-controller-manager)  : Runs {{< glossary_tooltip text="controllers" term_id=…
- `['Core Components', 'Control Plane Components', 'Node Components']`
  > Node Components  Run on every node, maintaining running pods and providing the Kubernetes runtime environment:  [kubelet](/docs/concepts/architecture/#kubelet)  : Ensures that Pods are running, including their containers.  [kube-proxy](/docs/concepts/architecture/#kube-proxy) (optional)  : Maintains network rules on nodes to implement {{< glossary_tooltip text="Services" term_id="service" >}}.  [Container runtime](/docs/concepts/architecture/#container-runtime)  : Software responsible for running containers. Read  [Container Runtimes](/docs/setup/production-environment/container-runtimes/) to…
- `['Core Components', 'Addons']`
  > Addons  Addons extend the functionality of Kubernetes. A few important examples include:  [DNS](/docs/concepts/architecture/#dns)  : For cluster-wide DNS resolution.  [Web UI](/docs/concepts/architecture/#web-ui-dashboard) (Dashboard)  : For cluster management via a web interface.  [Container Resource Monitoring](/docs/concepts/architecture/#container-resource-monitoring)  : For collecting and storing container metrics.  [Cluster-level Logging](/docs/concepts/architecture/#cluster-level-logging)  : For saving container logs to a central log store.
- `['Core Components', 'Flexibility in Architecture']`
  > Flexibility in Architecture  Kubernetes allows for flexibility in how these components are deployed and managed.  The architecture can be adapted to various needs, from small development environments  to large-scale production deployments.  For more detailed information about each component and various ways to configure your  cluster architecture, see the [Cluster Architecture](/docs/concepts/architecture/) page.

`verdict_relevant:` ______   `notes:` ______

---

## 15. `4e9976b05ac0010c`  (git)

**Query** (zh / command): 怎么用 git branch 创建、重命名和删除分支？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-branch.adoc`

**Claimed section** (unverified): `['git-branch', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-branch(1)  =============  NAME  ----  git-branch - List, create, or delete branches  SYNOPSIS  --------  [synopsis]  git branch [--color[=<when>] | --no-color] [--show-current]  [-v [--abbrev=<n> | --no-abbrev]]  [--column[=<options>] | --no-column] [--sort=<key>]  [--merged [<commit>]] [--no-merged [<commit>]]  [--contains [<commit>]] [--no-contains [<commit>]]  [--points-at <object>] [--format=<format>]  [(-r|--remotes) | (-a|--all)]  [--list] [<pattern>...]  git branch [--track[=(direct|inherit)] | --no-track] [-f]  [--recurse-submodules] <branch-name> [<start-point>]  git branch (--set…
- `[]`
  > `branch.sort` variable if it exists, or to sorting based on the  full refname (including `refs/...` prefix). This lists  detached `HEAD` (if present) first, then local branches and  finally remote-tracking branches. See linkgit:git-config[1].  `-r`::  `--remotes`::  List or delete (if used with `-d`) the remote-tracking branches.  Combine with `--list` to match the optional pattern(s).  `-a`::  `--all`::  List both remote-tracking branches and local branches.  Combine with `--list` to match optional pattern(s).  `-l`::  `--list`::  List branches. With optional `<pattern>...`, e.g. `git  branch…
- `[]`
  > `git fetch` or `git pull` will create them again unless you configure them not to.  See linkgit:git-fetch[1].  <2> Delete the "test" branch even if the "master" branch (or whichever branch  is currently checked out) does not have all commits from the test branch.  Listing branches from a specific remote::  +  ------------  $ git branch -r -l '<remote>/<pattern>' <1>  $ git for-each-ref 'refs/remotes/<remote>/<pattern>' <2>  ------------  +  <1> Using `-a` would conflate _<remote>_ with any local branches you happen to  have been prefixed with the same _<remote>_ pattern.  <2> `for-each-ref` ca…

`verdict_relevant:` ______   `notes:` ______

---

## 16. `f8fa6e5656c5cc3e`  (postgresql)

**Query** (zh / code_api): 怎么用 libpq 建立数据库连接并执行查询？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/libpq.sgml`

**Claimed section** (unverified): `['libpq — C Library', 'Database Connection Control Functions']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/libpq.sgml -->  <chapter id="libpq">  <title><application>libpq</application> &mdash; C Library</title>  <indexterm zone="libpq">  <primary>libpq</primary>  </indexterm>  <indexterm zone="libpq">  <primary>C</primary>  </indexterm>  <para>  <application>libpq</application> is the <acronym>C</acronym>  application programmer's interface to <productname>PostgreSQL</productname>.  <application>libpq</application> is a set of library functions that allow  client programs to pass queries to the <productname>PostgreSQL</productname>  backend server and to receive the results of the…
- `[]`
  > This is the predecessor of <xref linkend="libpq-PQconnectdb"/> with a fixed  set of parameters. It has the same functionality except that the  missing parameters will always take on default values. Write <symbol>NULL</symbol> or an  empty string for any one of the fixed parameters that is to be defaulted.  </para>  <para>  If the <parameter>dbName</parameter> contains  an <symbol>=</symbol> sign or has a valid connection <acronym>URI</acronym> prefix, it  is taken as a <parameter>conninfo</parameter> string in exactly the same way as  if it had been passed to <xref linkend="libpq-PQconnectdb"/…
- `[]`
  > or <xref linkend="libpq-PQconnectStartParams"/> returns a non-null  pointer, you must call <xref linkend="libpq-PQfinish"/> when you are  finished with it, in order to dispose of the structure and any  associated memory blocks. This must be done even if the connection  attempt fails or is abandoned.  </para>  </listitem>  </varlistentry>  <varlistentry id="libpq-PQsocketPoll">  <term><function>PQsocketPoll</function><indexterm><primary>PQsocketPoll</primary></indexterm></term>  <listitem>  <para>  <indexterm><primary>nonblocking connection</primary></indexterm>  Poll a connection's underlying…
- `[]`
  > Reset the communication channel to the server, in a nonblocking manner.  <synopsis>  int PQresetStart(PGconn *conn);  PostgresPollingStatusType PQresetPoll(PGconn *conn);  </synopsis>  </para>  <para>  These functions will close the connection to the server and attempt to  establish a new connection, using all the same  parameters previously used. This can be useful for error recovery if a  working connection is lost. They differ from <xref linkend="libpq-PQreset"/> (above) in that they  act in a nonblocking manner. These functions suffer from the same  restrictions as <xref linkend="libpq-PQc…
- `[]`
  > The host part may be either a host name or an IP address. To specify an  IPv6 address, enclose it in square brackets:  <synopsis>  postgresql://[2001:db8::1234]/database  </synopsis>  </para>  <para>  The host part is interpreted as described for the parameter <xref  linkend="libpq-connect-host"/>. In particular, a Unix-domain socket  connection is chosen if the host part is either empty or looks like an  absolute path name,  otherwise a TCP/IP connection is initiated. Note, however, that the  slash is a reserved character in the hierarchical part of the URI. So, to  specify a non-standard Uni…
- `[]`
  > <term><literal>port</literal></term>  <listitem>  <para>  Port number to connect to at the server host, or socket file  name extension for Unix-domain  connections.<indexterm><primary>port</primary></indexterm>  If multiple hosts were given in the <literal>host</literal> or  <literal>hostaddr</literal> parameters, this parameter may specify a  comma-separated list of ports of the same length as the host list, or  it may specify a single port number to be used for all hosts.  An empty string, or an empty item in a comma-separated list,  specifies the default port number established  when <produ…

`verdict_relevant:` ______   `notes:` ______

---

## 17. `869f83b528bb4e40`  (kubernetes)

**Query** (zh / command): 怎么检查节点的健康状态和条件（condition）？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/debug/debug-cluster/monitor-node-health.md`

**Claimed section** (unverified): `['Monitor Node Health']`

**Actual sections in the index:**

- `[]`
  > *Node Problem Detector* is a daemon for monitoring and reporting about a node's health.  You can run Node Problem Detector as a `DaemonSet` or as a standalone daemon.  Node Problem Detector collects information about node problems from various daemons  and reports these conditions to the API server as Node [Condition](/docs/concepts/architecture/nodes/#condition)s  or as [Event](/docs/reference/kubernetes-api/cluster-resources/event-v1)s.  To learn how to install and use Node Problem Detector, see  [Node Problem Detector project documentation](https://github.com/kubernetes/node-problem-detecto…
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}
- `['{{% heading "prerequisites" %}}', 'Limitations']`
  > Limitations  * Node Problem Detector uses the kernel log format for reporting kernel issues.  To learn how to extend the kernel log format, see [Add support for another log format](#support-other-log-format).
- `['{{% heading "prerequisites" %}}', 'Enabling Node Problem Detector']`
  > Enabling Node Problem Detector  Some cloud providers enable Node Problem Detector as an {{< glossary_tooltip text="Addon" term_id="addons" >}}.  You can also enable Node Problem Detector with `kubectl` or by creating an Addon DaemonSet.
- `['{{% heading "prerequisites" %}}', 'Enabling Node Problem Detector', 'Using kubectl to enable Node Problem Detector {#using-kubectl}']`
  > Using kubectl to enable Node Problem Detector {#using-kubectl}  `kubectl` provides the most flexible management of Node Problem Detector.  You can overwrite the default configuration to fit it into your environment or  to detect customized node problems. For example:  1. Create a Node Problem Detector configuration similar to `node-problem-detector.yaml`:  {{% code_sample file="debug/node-problem-detector.yaml" %}}  {{< note >}}  You should verify that the system log directory is right for your operating system distribution.  {{< /note >}}  1. Start node problem detector with `kubectl`:  kubec…
- `['{{% heading "prerequisites" %}}', 'Enabling Node Problem Detector', 'Using an Addon pod to enable Node Problem Detector {#using-addon-pod}']`
  > Using an Addon pod to enable Node Problem Detector {#using-addon-pod}  If you are using a custom cluster bootstrap solution and don't need  to overwrite the default configuration, you can leverage the Addon pod to  further automate the deployment.  Create `node-problem-detector.yaml`, and save the configuration in the Addon pod's  directory `/etc/kubernetes/addons/node-problem-detector` on a control plane node.

`verdict_relevant:` ______   `notes:` ______

---

## 18. `f2e3096d12da7365`  (kubernetes)

**Query** (zh / config): StorageClass 是干什么的，动态供给是怎么工作的？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/storage/storage-classes.md`

**Claimed section** (unverified): `['Storage Classes']`

**Actual sections in the index:**

- `[]`
  > This document describes the concept of a StorageClass in Kubernetes. Familiarity  with [volumes](/docs/concepts/storage/volumes/) and  [persistent volumes](/docs/concepts/storage/persistent-volumes) is suggested.  A StorageClass provides a way for administrators to describe the _classes_ of  storage they offer. Different classes might map to quality-of-service levels,  or to backup policies, or to arbitrary policies determined by the cluster  administrators. Kubernetes itself is unopinionated about what classes  represent.  The Kubernetes concept of a storage class is similar to “profiles” in…
- `['StorageClass objects']`
  > StorageClass objects  Each StorageClass contains the fields `provisioner`, `parameters`, and  `reclaimPolicy`, which are used when a PersistentVolume belonging to the  class needs to be dynamically provisioned to satisfy a PersistentVolumeClaim (PVC).  The name of a StorageClass object is significant, and is how users can  request a particular class. Administrators set the name and other parameters  of a class when first creating StorageClass objects.  As an administrator, you can specify a default StorageClass that applies to any PVCs that  don't request a specific class. For more details, se…
- `['StorageClass objects', 'Default StorageClass']`
  > Default StorageClass  You can mark a StorageClass as the default for your cluster.  For instructions on setting the default StorageClass, see  [Change the default StorageClass](/docs/tasks/administer-cluster/change-default-storage-class/).  When a PVC does not specify a `storageClassName`, the default StorageClass is  used.  If you set the  [`storageclass.kubernetes.io/is-default-class`](/docs/reference/labels-annotations-taints/#storageclass-kubernetes-io-is-default-class)  annotation to true on more than one StorageClass in your cluster, and you then  create a PersistentVolumeClaim with no `…
- `['StorageClass objects', 'Provisioner']`
  > Provisioner  Each StorageClass has a provisioner that determines what volume plugin is used  for provisioning PVs. This field must be specified.  | Volume Plugin        | Internal Provisioner |            Config Example             |  | :------------------- | :------------------: | :-----------------------------------: |  | AzureFile            |       &#x2713;       |       [Azure File](#azure-file)       |  | CephFS               |          -           |                   -                   |  | FC                   |          -           |                   -                   |  | FlexVol…
- `['StorageClass objects', 'Reclaim policy']`
  > Reclaim policy  PersistentVolumes that are dynamically created by a StorageClass will have the  [reclaim policy](/docs/concepts/storage/persistent-volumes/#reclaiming)  specified in the `reclaimPolicy` field of the class, which can be  either `Delete` or `Retain`. If no `reclaimPolicy` is specified when a  StorageClass object is created, it will default to `Delete`.  PersistentVolumes that are created manually and managed via a StorageClass will have  whatever reclaim policy they were assigned at creation.
- `['StorageClass objects', 'Volume expansion {#allow-volume-expansion}']`
  > Volume expansion {#allow-volume-expansion}  PersistentVolumes can be configured to be expandable. This allows you to resize the  volume by editing the corresponding PVC object, requesting a new larger amount of  storage.  The following types of volumes support volume expansion, when the underlying  StorageClass has the field `allowVolumeExpansion` set to true.  {{< table caption = "Table of Volume types and the version of Kubernetes they require"  >}}  | Volume type          | Required Kubernetes version for volume expansion |  | :------------------- | :----------------------------------------…

`verdict_relevant:` ______   `notes:` ______

---

## 19. `25761f0d423a5715`  (python)

**Query** (zh / concept): 为什么在循环里定义的 lambda 最后都返回同一个值？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/faq/programming.rst`

**Claimed section** (unverified): `['Programming FAQ', 'Core language']`

**Actual sections in the index:**

- `[]`
  > :tocdepth: 2  ===============  Programming FAQ  ===============  .. only:: html  .. contents::  General questions  =================  Is there a source code-level debugger with breakpoints and single-stepping?  ---------------------------------------------------------------------------  Yes.  Several debuggers for Python are described below, and the built-in function  :func:`breakpoint` allows you to drop into any of them.  The pdb module is a simple but adequate console-mode debugger for Python. It is  part of the standard Python library, and is :mod:`documented in the Library  Reference Manu…
- `[]`
  > Assume you use a for loop to define a few different lambdas (or even plain  functions), for example::  >>> squares = []  >>> for x in range(5):  ... squares.append(lambda: x**2)  This gives you a list that contains 5 lambdas that calculate ``x**2``. You  might expect that, when called, they would return, respectively, ``0``, ``1``,  ``4``, ``9``, and ``16``. However, when you actually try you will see that  they all return ``16``::  >>> squares[2]()  16  >>> squares[4]()  16  This happens because ``x`` is not local to the lambdas, but is defined in  the outer scope, and it is accessed when the…
- `[]`
  > and class instances can lead to confusion.  Because of this feature, it is good programming practice to not use mutable  objects as default values. Instead, use ``None`` as the default value and  inside the function, check if the parameter is ``None`` and create a new  list/dictionary/whatever if it is. For example, don't write::  def foo(mydict={}):  ...  but::  def foo(mydict=None):  if mydict is None:  mydict = {} # create a new dict for local namespace  This feature can be useful. When you have a function that's time-consuming to  compute, a common technique is to cache the parameters and…
- `['Callers can only provide two parameters and optionally pass _cache by keyword']`
  > Callers can only provide two parameters and optionally pass _cache by keyword  def expensive(arg1, arg2, *, _cache={}):  if (arg1, arg2) in _cache:  return _cache[(arg1, arg2)]
- `['Calculate the value']`
  > Calculate the value  result = ... expensive computation ...  _cache[(arg1, arg2)] = result # Store result in the cache  return result  You could use a global variable containing a dictionary instead of the default  value; it's a matter of taste.  How can I pass optional or keyword parameters from one function to another?  ---------------------------------------------------------------------------  Collect the arguments using the ``*`` and ``**`` specifiers in the function's  parameter list; this gives you the positional arguments as a tuple and the  keyword arguments as a dictionary. You can t…
- `['Calculate the value']`
  > ...  >>> def func4(args):  ... args.a = 'new-value' # args is a mutable Namespace  ... args.b = args.b + 1 # change object in-place  ...  >>> args = Namespace(a='old-value', b=99)  >>> func4(args)  >>> vars(args)  {'a': 'new-value', 'b': 100}  There's almost never a good reason to get this complicated.  Your best choice is to return a tuple containing the multiple results.  How do you make a higher order function in Python?  --------------------------------------------------  You have two choices: you can use nested scopes or you can use callable objects.  For example, suppose you wanted to de…

`verdict_relevant:` ______   `notes:` ______

---

## 20. `09dee657668e3b84`  (python)

**Query** (zh / config): 怎么配置 Python logging 的日志级别和输出格式？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/howto/logging.rst`

**Claimed section** (unverified): `['Logging HOWTO']`

**Actual sections in the index:**

- `[]`
  > .. _logging-howto:  =============  Logging HOWTO  =============  .. _logging-basic-tutorial:  .. currentmodule:: logging  This page contains tutorial information. For links to reference information and a  logging cookbook, please see :ref:`tutorial-ref-links`.  Basic Logging Tutorial  ----------------------  Logging is a means of tracking events that happen when some software runs. The  software's developer adds logging calls to their code to indicate that certain  events have occurred. An event is described by a descriptive message which can  optionally contain variable data (i.e. data that i…
- `[]`
  > This example also shows how you can set the logging level which acts as the  threshold for tracking. In this case, because we set the threshold to  ``DEBUG``, all of the messages were printed.  If you want to set the logging level from a command-line option such as:  .. code-block:: none  --log=INFO  and you have the value of the parameter passed for ``--log`` in some variable  *loglevel*, you can use::  getattr(logging, loglevel.upper())  to get the value which you'll pass to :func:`basicConfig` via the *level*  argument. You may want to error check any user input value, perhaps as in the  fo…
- `['assuming loglevel is bound to the string value obtained from the']`
  > assuming loglevel is bound to the string value obtained from the
- `['command line argument. Convert to upper case to allow the user to']`
  > command line argument. Convert to upper case to allow the user to
- `['specify --log=DEBUG or --log=debug']`
  > specify --log=DEBUG or --log=debug  numeric_level = getattr(logging, loglevel.upper(), None)  if not isinstance(numeric_level, int):  raise ValueError('Invalid log level: %s' % loglevel)  logging.basicConfig(level=numeric_level, ...)  The call to :func:`basicConfig` should come *before* any calls to a logger's  methods such as :meth:`~Logger.debug`, :meth:`~Logger.info`, etc. Otherwise,  that logging event may not be handled in the desired manner.  If you run the above script several times, the messages from successive runs  are appended to the file *example.log*. If you want each run to start…
- `['specify --log=DEBUG or --log=debug']`
  > tutorial examples. If you call the functions :func:`debug`, :func:`info`,  :func:`warning`, :func:`error` and :func:`critical`, they will check to see  if no destination is set; and if one is not set, they will set a destination  of the console (``sys.stderr``) and a default format for the displayed  message before delegating to the root logger to do the actual message output.  The default format set by :func:`basicConfig` for messages is:  .. code-block:: none  severity:logger name:message  You can change this by passing a format string to :func:`basicConfig` with the  *format* keyword argume…

`verdict_relevant:` ______   `notes:` ______

---

## 21. `5ffc87ac143d677b`  (docker)

**Query** (en / concept): What is a container registry and what role does it play in image distribution?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/get-started/docker-concepts/the-basics/what-is-a-registry.md`

**Claimed section** (unverified): `['What is a registry?']`

**Actual sections in the index:**

- `[]`
  > {{< youtube-embed 2WDl10Wv5rs >}}
- `['Explanation']`
  > Explanation  Now that you know what a container image is and how it works, you might wonder - where do you store these images?  Well, you can store your container images on your computer system, but what if you want to share them with your friends or use them on another machine? That's where the image registry comes in.  An image registry is a centralized location for storing and sharing your container images. It can be either public or private. [Docker Hub](https://hub.docker.com) is a public registry that anyone can use and is the default registry.  While Docker Hub is a popular option, ther…
- `['Explanation', 'Registry vs. repository']`
  > Registry vs. repository  While you're working with registries, you might hear the terms _registry_ and _repository_ as if they're interchangeable. Even though they're related, they're not quite the same thing.  A _registry_ is a centralized location that stores and manages container images, whereas a _repository_ is a collection of related container images within a registry. Think of it as a folder where you organize your images based on projects. Each repository contains one or more container images.  The following diagram shows the relationship between a registry, repositories, and images.…
- `['Explanation', 'Try it out']`
  > Try it out  In this hands-on, you will learn how to build and push a Docker image to the Docker Hub repository.
- `['Explanation', 'Try it out', 'Sign up for a free Docker account']`
  > Sign up for a free Docker account  1. If you haven't created one yet, head over to the [Docker Hub](https://hub.docker.com) page to sign up for a new Docker account. Be sure to finish the verification steps sent to your email.  ![Screenshot of the official Docker Hub page showing the Sign up page](images/dockerhub-signup.webp?border)  You can use your Google or GitHub account to authenticate.
- `['Explanation', 'Try it out', 'Create your first repository']`
  > Create your first repository  1. Sign in to [Docker Hub](https://hub.docker.com).  2. Select the **Create repository** button in the top-right corner.  3. Select your namespace (most likely your username) and enter `docker-quickstart` as the repository name.  ![Screenshot of the Docker Hub page that shows how to create a public repository](images/create-hub-repository.webp?border)  4. Set the visibility to **Public**.  5. Select the **Create** button to create the repository.  That's it. You've successfully created your first repository. 🎉  This repository is empty right now. You'll now fix th…

`verdict_relevant:` ______   `notes:` ______

---

## 22. `dde536ea7d14f3a7`  (kubernetes)

**Query** (en / config): How do you manage Secrets using a configuration file?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/configmap-secret/managing-secret-using-config-file.md`

**Claimed section** (unverified): `['Managing Secrets using Configuration File']`

**Actual sections in the index:**

- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}
- `['{{% heading "prerequisites" %}}', 'Create the Secret {#create-the-config-file}']`
  > Create the Secret {#create-the-config-file}  You can define the `Secret` object in a manifest first, in JSON or YAML format,  and then create that object. The  [Secret](/docs/reference/generated/kubernetes-api/{{< param "version" >}}/#secret-v1-core)  resource contains two maps: `data` and `stringData`.  The `data` field is used to store arbitrary data, encoded using base64. The  `stringData` field is provided for convenience, and it allows you to provide  the same data as unencoded strings.  The keys of `data` and `stringData` must consist of alphanumeric characters,  `-`, `_` or `.`.  The fo…
- `['{{% heading "prerequisites" %}}', 'Create the Secret {#create-the-config-file}', 'Specify unencoded data when creating a Secret']`
  > Specify unencoded data when creating a Secret  For certain scenarios, you may wish to use the `stringData` field instead. This  field allows you to put a non-base64 encoded string directly into the Secret,  and the string will be encoded for you when the Secret is created or updated.  A practical example of this might be where you are deploying an application  that uses a Secret to store a configuration file, and you want to populate  parts of that configuration file during your deployment process.  For example, if your application uses the following configuration file:  apiUrl: "https://my.ap…
- `['{{% heading "prerequisites" %}}', 'Create the Secret {#create-the-config-file}', 'Specify both `data` and `stringData`']`
  > Specify both `data` and `stringData`  If you specify a field in both `data` and `stringData`, the value from `stringData` is used.  For example, if you define the following Secret:  apiVersion: v1  kind: Secret  metadata:  name: mysecret  type: Opaque  data:  username: YWRtaW4=  stringData:  username: administrator  {{< note >}}  The `stringData` field for a Secret does not work well with server-side apply.  {{< /note >}}  The `Secret` object is created as follows:  apiVersion: v1  data:  username: YWRtaW5pc3RyYXRvcg==  kind: Secret  metadata:  creationTimestamp: 2018-11-15T20:46:46Z  name: my…
- `['{{% heading "prerequisites" %}}', 'Edit a Secret {#edit-secret}']`
  > Edit a Secret {#edit-secret}  To edit the data in the Secret you created using a manifest, modify the `data`  or `stringData` field in your manifest and apply the file to your  cluster. You can edit an existing `Secret` object unless it is  [immutable](/docs/concepts/configuration/secret/#secret-immutable).  For example, if you want to change the password from the previous example to  `birdsarentreal`, do the following:  1. Encode the new password string:  echo -n 'birdsarentreal' | base64  The output is similar to:  YmlyZHNhcmVudHJlYWw=  1. Update the `data` field with your new password strin…
- `['{{% heading "prerequisites" %}}', 'Clean up']`
  > Clean up  To delete the Secret you have created:  kubectl delete secret mysecret

`verdict_relevant:` ______   `notes:` ______

---

## 23. `5d5852f15d0735c6`  (docker)

**Query** (en / command): How do you filter docker command output, for example listing only running containers?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/cli/filter.md`

**Claimed section** (unverified): `['Filter commands']`

**Actual sections in the index:**

- `[]`
  > You can use the `--filter` flag to scope your commands. When filtering, the  commands only include entries that match the pattern you specify.
- `['Using filters']`
  > Using filters  The `--filter` flag expects a key-value pair separated by an operator.  $ docker COMMAND --filter "KEY=VALUE"  The key represents the field that you want to filter on.  The value is the pattern that the specified field must match.  The operator can be either equals (`=`) or not equals (`!=`).  For example, the command `docker images --filter reference=alpine` filters the  output of the `docker images` command to only print `alpine` images.  $ docker images  REPOSITORY   TAG       IMAGE ID       CREATED          SIZE  ubuntu       24.04     33a5cc25d22c   36 minutes ago   101MB…
- `['Using filters', 'Combining filters']`
  > Combining filters  You can combine multiple filters by passing multiple `--filter` flags. The  following example shows how to print all images that match `alpine:latest` or  `busybox` - a logical `OR`.  $ docker images  REPOSITORY   TAG       IMAGE ID       CREATED       SIZE  ubuntu       24.04     33a5cc25d22c   2 hours ago   101MB  ubuntu       22.04     152dc042452c   2 hours ago   88.1MB  alpine       3.21      a8cbb8c69ee7   2 hours ago   8.67MB  alpine       latest    7144f7bab3d4   2 hours ago   11.7MB  busybox      uclibc    3e516f71d880   2 hours ago   2.4MB  busybox      glibc     7…
- `['Using filters', 'Combining filters', 'Multiple negated filters']`
  > Multiple negated filters  Some commands support negated filters on [labels](/manuals/engine/manage-resources/labels.md).  Negated filters only consider results that don't match the specified patterns.  The following command prunes all containers that aren't labeled `foo`.  $ docker container prune --filter "label!=foo"  There's a catch in combining multiple negated label filters. Multiple negated  filters create a single negative constraint - a logical `AND`. The following  command prunes all containers except those labeled both `foo` and `bar`.  Containers labeled either `foo` or `bar`, but n…
- `['Using filters', 'Reference']`
  > Reference  For more information about filtering commands, refer to the CLI reference  description for commands that support the `--filter` flag:  - [`docker config ls`](/reference/cli/docker/config/ls/)  - [`docker container prune`](/reference/cli/docker/container/prune/)  - [`docker image prune`](/reference/cli/docker/image/prune/)  - [`docker image ls`](/reference/cli/docker/image/ls/)  - [`docker network ls`](/reference/cli/docker/network/ls/)  - [`docker network prune`](/reference/cli/docker/network/prune/)  - [`docker node ls`](/reference/cli/docker/node/ls/)  - [`docker node ps`](/refere…

`verdict_relevant:` ______   `notes:` ______

---

## 24. `0e13c7d910a99a9e`  (docker)

**Query** (en / concept): How do Compose services, networks, and volumes fit together in the application model?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/intro/compose-application-model.md`

**Claimed section** (unverified): `['How Compose works']`

**Actual sections in the index:**

- `[]`
  > With Docker Compose you use a YAML configuration file, known as the [Compose file](#the-compose-file), to configure your application’s services, and then you create and start all the services from your configuration with the [Compose CLI](#cli).  The Compose file, or `compose.yaml` file, follows the rules provided by the [Compose Specification](/reference/compose-file/_index.md) in how to define multi-container applications. This is the Docker Compose implementation of the formal [Compose Specification](https://github.com/compose-spec/compose-spec).  {{< accordion title="The Compose applicatio…
- `['The Compose file']`
  > The Compose file  The default path for a Compose file is `compose.yaml` (preferred) or `compose.yml` that is placed in the working directory.  Compose also supports `docker-compose.yaml` and `docker-compose.yml` for backwards compatibility of earlier versions.  If both files exist, Compose prefers the canonical `compose.yaml`.  You can use [fragments](/reference/compose-file/fragments.md) and [extensions](/reference/compose-file/extension.md) to keep your Compose file efficient and easy to maintain.  Multiple Compose files can be [merged](/reference/compose-file/merge.md) together to define th…
- `['The Compose file', 'CLI']`
  > CLI  The Docker CLI lets you interact with your Docker Compose applications through the `docker compose` command and its subcommands. If you're using Docker Desktop, the Docker Compose CLI is included by default.  Using the CLI, you can manage the lifecycle of your multi-container applications defined in the `compose.yaml` file. The CLI commands enable you to start, stop, and configure your applications effortlessly.
- `['The Compose file', 'CLI', 'Key commands']`
  > Key commands  To start all the services defined in your `compose.yaml` file:  $ docker compose up  To stop and remove the running services:  $ docker compose down  If you want to monitor the output of your running containers and debug issues, you can view the logs with:  $ docker compose logs  To list all the services along with their current status:  $ docker compose ps  For a full list of all the Compose CLI commands, see the [reference documentation](/reference/cli/docker/compose/).
- `['The Compose file', 'Illustrative example']`
  > Illustrative example  The following example illustrates the Compose concepts outlined above. The example is non-normative.  Consider an application split into a frontend web application and a backend service.  The frontend is configured at runtime with an HTTP configuration file managed by infrastructure, providing an external domain name, and an HTTPS server certificate injected by the platform's secured secret store.  The backend stores data in a persistent volume.  Both services communicate with each other on an isolated back-tier network, while the frontend is also connected to a front-tie…
- `['The presence of these objects is sufficient to define them']`
  > The presence of these objects is sufficient to define them  front-tier: {}  back-tier: {}  The `docker compose up` command starts the `frontend` and `backend` services, creates the necessary networks and volumes, and injects the configuration and secret into the frontend service.  `docker compose ps` provides a snapshot of the current state of your services, making it easy to see which containers are running, their status, and the ports they are using:  $ docker compose ps  NAME                IMAGE                COMMAND                  SERVICE             CREATED             STATUS…

`verdict_relevant:` ______   `notes:` ______

---

## 25. `975d01bb7c26414f`  (git)

**Query** (en / command): How do you inspect commit history with git log and filter commits by author or by file?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-log.adoc`

**Claimed section** (unverified): `['git-log', 'OPTIONS']`

**Actual sections in the index:**

- `[]`
  > git-log(1)  ==========  NAME  ----  git-log - Show commit logs  SYNOPSIS  --------  [synopsis]  git log [<options>] [<revision-range>] [[--] <path>...]  DESCRIPTION  -----------  Shows the commit logs.  :git-log: 1  include::rev-list-description.adoc[]  The command takes options applicable to the linkgit:git-rev-list[1]  command to control what is shown and how, and options applicable to  the linkgit:git-diff[1] command to control how the changes  each commit introduces are shown.  OPTIONS  -------  `--follow`::  Continue listing the history of a file beyond renames  (works only for a single f…

`verdict_relevant:` ______   `notes:` ______

---

## 26. `b3124a54e22b2e75`  (go)

**Query** (zh / config): GODEBUG 里 panicnil 这个开关控制的是哪类行为？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `['Go, Backwards Compatibility, and GODEBUG', 'GODEBUG History']`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 27. `60fa5c3eac8042fc`  (postgresql)

**Query** (en / config): How do I set up the pg_stat_statements extension for query monitoring?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/ecpg.sgml`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/ecpg.sgml -->  <chapter id="ecpg">  <title><application>ECPG</application> &mdash; Embedded <acronym>SQL</acronym> in C</title>  <indexterm zone="ecpg"><primary>embedded SQL</primary><secondary>in C</secondary></indexterm>  <indexterm zone="ecpg"><primary>C</primary></indexterm>  <indexterm zone="ecpg"><primary>ECPG</primary></indexterm>  <para>  This chapter describes the embedded <acronym>SQL</acronym> package  for <productname>PostgreSQL</productname>. It was written by  Linus Tolke (<email>linus@epact.se</email>) and Michael Meskes  (<email>meskes@postgresql.org</email>).…
- `[]`
  > variables can be used in SQL statements when you prefix them with a  colon.  </para>  <para>  Be advised that the format of the connection target is not  specified in the SQL standard. So if you want to develop portable  applications, you might want to use something based on the last  example above to encapsulate the connection target string  somewhere.  </para>  <para>  If untrusted users have access to a database that has not adopted a  <link linkend="ddl-schemas-patterns">secure schema usage pattern</link>,  begin each session by removing publicly-writable schemas  from <varname>search_path…
- `['include <stdio.h>']`
  > include <stdio.h>  EXEC SQL BEGIN DECLARE SECTION;  char dbname[1024];  EXEC SQL END DECLARE SECTION;  int  main()  {  EXEC SQL CONNECT TO testdb1 AS con1 USER testuser;  EXEC SQL SELECT pg_catalog.set_config('search_path', '', false); EXEC SQL COMMIT;  EXEC SQL CONNECT TO testdb2 AS con2 USER testuser;  EXEC SQL SELECT pg_catalog.set_config('search_path', '', false); EXEC SQL COMMIT;  EXEC SQL CONNECT TO testdb3 AS con3 USER testuser;  EXEC SQL SELECT pg_catalog.set_config('search_path', '', false); EXEC SQL COMMIT;  /* This query would be executed in the last opened database "testdb3". */  E…
- `['include <stdio.h>']`
  > <para>  Enable autocommit mode.  </para>  </listitem>  </varlistentry>  <varlistentry id="ecpg-transactions-exec-sql-autocommit-off">  <term><literal>EXEC SQL SET AUTOCOMMIT TO OFF</literal></term>  <listitem>  <para>  Disable autocommit mode. This is the default.  </para>  </listitem>  </varlistentry>  </variablelist>  </para>  </sect2>  <sect2 id="ecpg-prepared">  <title>Prepared Statements</title>  <para>  When the values to be passed to an SQL statement are not known at  compile time, or the same statement is going to be used many  times, then prepared statements can be useful.  </para>  <…
- `['include <stdio.h>']`
  > Here is an example using the command <command>FETCH</command>:  <programlisting>  EXEC SQL BEGIN DECLARE SECTION;  int v1;  VARCHAR v2;  EXEC SQL END DECLARE SECTION;  ...  EXEC SQL DECLARE foo CURSOR FOR SELECT a, b FROM test;  ...  do  {  ...  EXEC SQL FETCH NEXT FROM foo INTO :v1, :v2;  ...  } while (...);  </programlisting>  Here the <literal>INTO</literal> clause appears after all the  normal clauses.  </para>  </sect2>  <sect2 id="ecpg-variables-type-mapping">  <title>Type Mapping</title>  <para>  When ECPG applications exchange values between the PostgreSQL  server and the C application…
- `['include &lt;pgtypes_timestamp.h>']`
  > include &lt;pgtypes_timestamp.h>  </programlisting>  </para>  <para>  Next, declare a host variable as type <type>timestamp</type> in  the declare section:  <programlisting>  EXEC SQL BEGIN DECLARE SECTION;  timestamp ts;  EXEC SQL END DECLARE SECTION;  </programlisting>  </para>  <para>  And after reading a value into the host variable, process it  using pgtypes library functions. In following example, the  <type>timestamp</type> value is converted into text (ASCII) form  with the <function>PGTYPEStimestamp_to_asc()</function>  function:  <programlisting>  EXEC SQL SELECT now()::timestamp INT…

`verdict_relevant:` ______   `notes:` ______

---

## 28. `22e19f825aa242b1`  (docker)

**Query** (en / config): How does Docker Compose control the startup and shutdown order of services?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/how-tos/startup-order.md`

**Claimed section** (unverified): `['Control startup and shutdown order in Compose']`

**Actual sections in the index:**

- `[]`
  > You can control the order of service startup and shutdown with the  [depends_on](/reference/compose-file/services.md#depends_on) attribute. Compose always starts and stops  containers in dependency order, where dependencies are determined by  `depends_on`, `links`, `volumes_from`, and `network_mode: "service:..."`.  For example, if your application needs to access a database and both services are started with `docker compose up`, there is a chance this will fail since the application service might start before the database service and won't find a database able to handle its SQL statements.
- `['Control startup']`
  > Control startup  On startup, Compose does not wait until a container is "ready", only until it's running. This can cause issues if, for example, you have a relational database system that needs to start its own services before being able to handle incoming connections.  The solution for detecting the ready state of a service is  to use the `condition` attribute with one of the following options:  - `service_started`  - `service_healthy`. This specifies that a dependency is expected to be “healthy”, which is defined with `healthcheck`, before starting a dependent service.  - `service_completed_…
- `['Control startup', 'Example']`
  > Example  services:  web:  build: .  depends_on:  db:  condition: service_healthy  restart: true  redis:  condition: service_started  redis:  image: redis  db:  image: postgres:18  healthcheck:  test: ["CMD-SHELL", "pg_isready -U $${POSTGRES_USER} -d $${POSTGRES_DB}"]  interval: 10s  retries: 5  start_period: 30s  timeout: 10s  Compose creates services in dependency order. `db` and `redis` are created before `web`.  Compose waits for healthchecks to pass on dependencies marked with `service_healthy`. `db` is expected to be "healthy" (as indicated by `healthcheck`) before `web` is created.  `res…
- `['Control startup', 'Reference information']`
  > Reference information  - [`depends_on`](/reference/compose-file/services.md#depends_on)  - [`healthcheck`](/reference/compose-file/services.md#healthcheck)

`verdict_relevant:` ______   `notes:` ______

---

## 29. `4f62769aa04f11e4`  (kubernetes)

**Query** (en / concept): How do labels and selectors work together to organize Kubernetes objects?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/overview/working-with-objects/labels.md`

**Claimed section** (unverified): `['Labels and Selectors']`

**Actual sections in the index:**

- `[]`
  > _Labels_ are key/value pairs that are attached to  {{< glossary_tooltip text="objects" term_id="object" >}} such as Pods.  Labels are intended to be used to specify identifying attributes of objects  that are meaningful and relevant to users, but do not directly imply semantics  to the core system. Labels can be used to organize and to select subsets of  objects. Labels can be attached to objects at creation time and subsequently  added and modified at any time. Each object can have a set of key/value labels  defined. Each Key must be unique for a given object.  "metadata": {  "labels": {  "ke…
- `['Motivation']`
  > Motivation  Labels enable users to map their own organizational structures onto system objects  in a loosely coupled fashion, without requiring clients to store these mappings.  Service deployments and batch processing pipelines are often multi-dimensional entities  (e.g., multiple partitions or deployments, multiple release tracks, multiple tiers,  multiple micro-services per tier). Management often requires cross-cutting operations,  which breaks encapsulation of strictly hierarchical representations, especially rigid  hierarchies determined by the infrastructure rather than by users.  Examp…
- `['Motivation', 'Syntax and character set']`
  > Syntax and character set  _Labels_ are key/value pairs. Valid label keys have two segments: an optional  prefix and name, separated by a slash (`/`). The name segment is required and  must be 63 characters or less, beginning and ending with an alphanumeric  character (`[a-z0-9A-Z]`) with dashes (`-`), underscores (`_`), dots (`.`),  and alphanumerics between. The prefix is optional. If specified, the prefix  must be a DNS subdomain: a series of DNS labels separated by dots (`.`),  not longer than 253 characters in total, followed by a slash (`/`).  If the prefix is omitted, the label Key is pr…
- `['Motivation', 'Label selectors']`
  > Label selectors  Unlike [names and UIDs](/docs/concepts/overview/working-with-objects/names/), labels  do not provide uniqueness. In general, we expect many objects to carry the same label(s).  Via a _label selector_, the client/user can identify a set of objects.  The label selector is the core grouping primitive in Kubernetes.  The API currently supports two types of selectors: _equality-based_ and _set-based_.  A label selector can be made of multiple _requirements_ which are comma-separated.  In the case of multiple requirements, all must be satisfied so the comma separator  acts as a logi…
- `['Motivation', 'Label selectors', '_Equality-based_ requirement']`
  > _Equality-based_ requirement  _Equality-_ or _inequality-based_ requirements allow filtering by label keys and values.  Matching objects must satisfy all of the specified label constraints, though they may  have additional labels as well. Three kinds of operators are admitted `=`,`==`,`!=`.  The first two represent _equality_ (and are synonyms), while the latter represents _inequality_.  For example:  environment = production  tier != frontend  The former selects all resources with key equal to `environment` and value equal to `production`.  The latter selects all resources with key equal to `…
- `['Motivation', 'Label selectors', '_Set-based_ requirement']`
  > _Set-based_ requirement  _Set-based_ label requirements allow filtering keys according to a set of values.  Three kinds of operators are supported: `in`,`notin` and `exists` (only the key identifier).  For example:  environment in (production, qa)  tier notin (frontend, backend)  partition  !partition  - The first example selects all resources with key equal to `environment` and value  equal to `production` or `qa`.  - The second example selects all resources with key equal to `tier` and values other  than `frontend` and `backend`, and all resources with no labels with the `tier` key.  - The t…

`verdict_relevant:` ______   `notes:` ______

---

## 30. `4d703ca3bd38934c`  (postgresql)

**Query** (en / command): How do you create a database cluster and start the PostgreSQL server?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/runtime.sgml`

**Claimed section** (unverified): `['Server Setup and Operation', 'Creating a Database Cluster']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/runtime.sgml -->  <chapter id="runtime">  <title>Server Setup and Operation</title>  <para>  This chapter discusses how to set up and run the database server,  and its interactions with the operating system.  </para>  <para>  The directions in this chapter assume that you are working with  plain <productname>PostgreSQL</productname> without any additional  infrastructure, for example a copy that you built from source  according to the directions in the preceding chapters.  If you are working with a pre-packaged or vendor-supplied  version of <productname>PostgreSQL</productna…
- `[]`
  > database and even become the database superuser. If you do not  trust other local users, we recommend you use one of  <command>initdb</command>'s <option>-W</option>, <option>--pwprompt</option>  or <option>--pwfile</option> options to assign a password to the  database superuser.<indexterm>  <primary>password</primary>  <secondary>of the superuser</secondary>  </indexterm>  Also, specify <option>-A scram-sha-256</option>  so that the default <literal>trust</literal> authentication  mode is not used; or modify the generated <filename>pg_hba.conf</filename>  file after running <command>initdb</…
- `[]`
  > background. For this, use the usual Unix shell syntax:  <screen>  $ <userinput>postgres -D /usr/local/pgsql/data &gt;logfile 2&gt;&amp;1 &amp;</userinput>  </screen>  It is important to store the server's <systemitem>stdout</systemitem> and  <systemitem>stderr</systemitem> output somewhere, as shown above. It will help  for auditing purposes and to diagnose problems. (See <xref  linkend="logfile-maintenance"/> for a more thorough discussion of log  file handling.)  </para>  <para>  The <command>postgres</command> program also takes a number of other  command-line options. For more information,…
- `[]`
  > increase the kernel limit.  </para>  <para>  Details about configuring <systemitem class="osname">System V</systemitem>  <acronym>IPC</acronym> facilities are given in <xref linkend="sysvipc"/>.  </para>  </sect2>  <sect2 id="client-connection-problems">  <title>Client Connection Problems</title>  <para>  Although the error conditions possible on the client side are quite  varied and application-dependent, a few of them might be directly  related to how the server was started. Conditions other than  those shown below should be documented with the respective client  application.  </para>  <para…
- `[]`
  > The runtime-computed parameter <xref linkend="guc-num-os-semaphores"/>  reports the number of semaphores required. This parameter can be viewed  before starting the server with a <command>postgres</command> command like:  <programlisting>  $ <userinput>postgres -D $PGDATA -C num_os_semaphores</userinput>  </programlisting>  </para>  <para>  Each set of 16 semaphores will  also contain a 17th semaphore which contains a <quote>magic  number</quote>, to detect collision with semaphore sets used by  other applications. The maximum number of semaphores in the system  is set by <varname>SEMMNS</varn…
- `[]`
  > <application>sysctl</application>. But it's still best to set up your preferred  values via <filename>/etc/sysctl.conf</filename>, so that the values will be  kept across reboots.  </para>  </listitem>  </varlistentry>  <varlistentry>  <term><systemitem class="osname">Solaris</systemitem></term>  <term><systemitem class="osname">illumos</systemitem></term>  <listitem>  <para>  The default shared memory and semaphore settings are usually good enough for most  <productname>PostgreSQL</productname> applications. Solaris defaults  to a <varname>SHMMAX</varname> of one-quarter of system <acronym>RA…

`verdict_relevant:` ______   `notes:` ______

---

## 31. `2935e5d6cb53455f`  (postgresql)

**Query** (zh / code_api): 怎么在 PL/pgSQL 里写触发器函数？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/plpgsql.sgml`

**Claimed section** (unverified): `['PL/pgSQL — SQL Procedural Language', 'Trigger Functions']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/plpgsql.sgml -->  <chapter id="plpgsql">  <title><application>PL/pgSQL</application> &mdash; <acronym>SQL</acronym> Procedural Language</title>  <indexterm zone="plpgsql">  <primary>PL/pgSQL</primary>  </indexterm>  <sect1 id="plpgsql-overview">  <title>Overview</title>  <para>  <application>PL/pgSQL</application> is a loadable procedural  language for the <productname>PostgreSQL</productname> database  system. The design goals of <application>PL/pgSQL</application> were to create  a loadable procedural language that  <itemizedlist>  <listitem>  <para>  can be used to create…
- `[]`
  > <literal>END</literal>, it must match the label at the block's beginning.  </para>  <para>  All key words are case-insensitive.  Identifiers are implicitly converted to lower case  unless double-quoted, just as they are in ordinary SQL commands.  </para>  <para>  Comments work the same way in <application>PL/pgSQL</application> code as in  ordinary SQL. A double dash (<literal>--</literal>) starts a comment  that extends to the end of the line. A <literal>/*</literal> starts a  block comment that extends to the matching occurrence of  <literal>*/</literal>. Block comments nest.  </para>  <para…
- `[]`
  > <literal>$<replaceable>n</replaceable></literal> names and optional  aliases in just the same way as the normal input parameters. An  output parameter is effectively a variable that starts out NULL;  it should be assigned to during the execution of the function.  The final value of the parameter is what is returned. For instance,  the sales-tax example could also be done this way:  <programlisting>  CREATE FUNCTION sales_tax(subtotal real, OUT tax real) AS $$  BEGIN  tax := subtotal * 0.06;  END;  $$ LANGUAGE plpgsql;  </programlisting>  Notice that we omitted <literal>RETURNS real</literal> &…
- `[]`
  > <literal>%TYPE</literal> is particularly valuable in polymorphic  functions, since the data types needed for internal variables can  change from one call to the next. Appropriate variables can be  created by applying <literal>%TYPE</literal> to the function's  arguments or result placeholders.  </para>  </sect2>  <sect2 id="plpgsql-declaration-rowtypes">  <title>Row Types</title>  <synopsis>  <replaceable>name</replaceable> <replaceable>table_name</replaceable><literal>%ROWTYPE</literal>;  <replaceable>name</replaceable> <replaceable>composite_type_name</replaceable>;  </synopsis>  <para>  A v…
- `[]`
  > really happens on first use of an expression is essentially a  <command>PREPARE</command> command. For example, if we have declared  two integer variables <literal>x</literal> and <literal>y</literal>, and we write  <programlisting>  IF x &lt; y THEN ...  </programlisting>  what happens behind the scenes is equivalent to  <programlisting>  PREPARE <replaceable>statement_name</replaceable>(integer, integer) AS SELECT $1 &lt; $2;  </programlisting>  and then this prepared statement is <command>EXECUTE</command>d for each  execution of the <command>IF</command> statement, with the current values…
- `[]`
  > and the plan is cached in the same way. Also, the special variable  <literal>FOUND</literal> is set to true if the query produced at  least one row, or false if it produced no rows (see  <xref linkend="plpgsql-statements-diagnostics"/>).  </para>  <note>  <para>  One might expect that writing <command>SELECT</command> directly  would accomplish this result, but at  present the only accepted way to do it is  <command>PERFORM</command>. An SQL command that can return rows,  such as <command>SELECT</command>, will be rejected as an error  unless it has an <literal>INTO</literal> clause as discuss…

`verdict_relevant:` ______   `notes:` ______

---

## 32. `7f7252c5d7671535`  (python)

**Query** (zh / troubleshooting): 程序里遇到 UnicodeEncodeError 或 UnicodeDecodeError 是为什么，怎么处理编码问题？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/howto/unicode.rst`

**Claimed section** (unverified): `['Introduction to Unicode', "Python's Unicode Support"]`

**Actual sections in the index:**

- `[]`
  > .. _unicode-howto:  *****************  Unicode HOWTO  *****************  This HOWTO discusses Python's support for the Unicode specification  for representing textual data, and explains various problems that  people commonly encounter when trying to work with Unicode.  Introduction to Unicode  =======================  Definitions  -----------  Today's programs need to be able to handle a wide variety of  characters. Applications are often internationalized to display  messages and output in a variety of user-selectable languages; the  same program might need to output an error message in Engli…
- `[]`
  > 5. If bytes are corrupted or lost, it's possible to determine the start of the  next UTF-8-encoded code point and resynchronize. It's also unlikely that  random 8-bit data will look like valid UTF-8.  6. UTF-8 is a byte oriented encoding. The encoding specifies that each  character is represented by a specific sequence of one or more bytes. This  avoids the byte-ordering issues that can occur with integer and word oriented  encodings, like UTF-16 and UTF-32, where the sequence of bytes varies depending  on the hardware on which the string was encoded.  References  ----------  The `Unicode Cons…
- `["'File not found' error message."]`
  > 'File not found' error message.  print("Fichier non trouvé")  Side note: Python 3 also supports using Unicode characters in identifiers::  répertoire = "/tmp/records.log"  with open(répertoire, "w") as f:  f.write("test\n")  If you can't enter a particular character in your editor or want to  keep the source code ASCII-only for some reason, you can also use  escape sequences in string literals. (Depending on your system,  you may see the actual capital-delta glyph instead of a \u escape.) ::  >>> "\N{GREEK CAPITAL LETTER DELTA}" # Using the character name  '\u0394'  >>> "\u0394" # Using a 16-b…
- `['!/usr/bin/env python']`
  > !/usr/bin/env python
- `['-*- coding: latin-1 -*-']`
  > -*- coding: latin-1 -*-  u = 'abcdé'  print(ord(u[-1]))  The syntax is inspired by Emacs's notation for specifying variables local to a  file. Emacs supports many different variables, but Python only supports  'coding'. The ``-*-`` symbols indicate to Emacs that the comment is special;  they have no significance to Python but are a convention. Python looks for  ``coding: name`` or ``coding=name`` in the comment.  If you don't include such a comment, the default encoding used will be UTF-8 as  already mentioned. See also :pep:`263` for more information.  .. _unicode-properties:  Unicode Propert…
- `['Get numeric value of second character']`
  > Get numeric value of second character  print(unicodedata.numeric(u[1]))  When run, this prints:  .. code-block:: none  0 00e9 Ll LATIN SMALL LETTER E WITH ACUTE  1 0bf2 No TAMIL NUMBER ONE THOUSAND  2 0f84 Mn TIBETAN MARK HALANTA  3 1770 Lo TAGBANWA LETTER SA  4 33af So SQUARE RAD OVER S SQUARED  1000.0  The category codes are abbreviations describing the nature of the character.  These are grouped into categories such as "Letter", "Number", "Punctuation", or  "Symbol", which in turn are broken up into subcategories. To take the codes  from the above output, ``'Ll'`` means 'Letter, lowercase',…

`verdict_relevant:` ______   `notes:` ______

---

## 33. `cc9cea6a5f87e624`  (git)

**Query** (zh / code_api): 怎么用 git format-patch 生成补丁文件发给别人？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-format-patch.adoc`

**Claimed section** (unverified): `['git-format-patch', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-format-patch(1)  ===================  NAME  ----  git-format-patch - Prepare patches for e-mail submission  SYNOPSIS  --------  [verse]  'git format-patch' [-k] [(-o|--output-directory) <dir> | --stdout]  [--no-thread | --thread[=<style>]]  [(--attach|--inline)[=<boundary>] | --no-attach]  [-s | --signoff]  [--signature=<signature> | --no-signature]  [--signature-file=<file>]  [-n | --numbered | -N | --no-numbered]  [--start-number <n>] [--numbered-files]  [--in-reply-to=<message-id>] [--suffix=.<sfx>]  [--ignore-if-in-upstream] [--always]  [--cover-from-description=<mode>]  [--rfc[=<rfc>]…
- `[]`
  > If `<mode>` is `message` or `default`, the cover letter subject will be  populated with placeholder text. The body of the cover letter will be  populated with the branch's description. This is the default mode when  no configuration nor command line option is specified.  +  If `<mode>` is `subject`, the first paragraph of the branch description will  populate the cover letter subject. The remainder of the description will  populate the body of the cover letter.  +  If `<mode>` is `auto`, if the first paragraph of the branch description  is greater than 100 bytes, then the mode will be `message…
- `[]`
  > the differences between the previous version of the patch series and  the series currently being formatted. `previous` is a single revision  naming the tip of the previous series which shares a common base with  the series being formatted (for example `git format-patch  --cover-letter --interdiff=feature/v1 -3 feature/v2`).  --range-diff=<previous>::  As a reviewer aid, insert a range-diff (see linkgit:git-range-diff[1])  into the cover letter, or as commentary of the lone patch of a  1-patch series, showing the differences between the previous  version of the patch series and the series curre…
- `[]`
  > title is likely to be different from the subject of the discussion the  patch is in response to, so it is likely that you would want to keep  the Subject: line, like the example above.  Checking for patch corruption  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~  Many mailers if not set up properly will corrupt whitespace. Here are  two common types of corruption:  * Empty context lines that do not have _any_ whitespace.  * Non-empty context lines that have one extra whitespace at the  beginning.  One way to test if your MUA is set up correctly is:  * Send the patch to yourself, exactly the way you would, exc…
- `[]`
  > testers to know the exact state the patch series applies to. It consists  of the 'base commit', which is a well-known commit that is part of the  stable part of the project history everybody else works off of, and zero  or more 'prerequisite patches', which are well-known patches in flight  that is not yet part of the 'base commit' that need to be applied on top  of 'base commit' in topological order before the patches can be applied.  The 'base commit' is shown as "base-commit: " followed by the 40-hex of  the commit object name. A 'prerequisite patch' is shown as  "prerequisite-patch-id: " f…

`verdict_relevant:` ______   `notes:` ______

---

## 34. `bcada255a63c04b5`  (docker)

**Query** (en / code_api): What options does the JSON File logging driver support?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/logging/drivers/json-file.md`

**Claimed section** (unverified): `['JSON File logging driver']`

**Actual sections in the index:**

- `[]`
  > By default, Docker captures the standard output (and standard error) of all your containers,  and writes them in files using the JSON format. The JSON format annotates each line with its  origin (`stdout` or `stderr`) and its timestamp. Each log file contains information about  only one container.  {  "log": "Log line is here\n",  "stream": "stdout",  "time": "2019-01-01T11:11:11.111111111Z"  }  > [!WARNING]  >  > The `json-file` logging driver uses file-based storage. These files are designed  > to be exclusively accessed by the Docker daemon. Interacting with these files  > with external too…
- `['Usage']`
  > Usage  To use the `json-file` driver as the default logging driver, set the `log-driver`  and `log-opts` keys to appropriate values in the `daemon.json` file. For more  information about configuring Docker using `daemon.json`, see  [daemon.json](/reference/cli/dockerd.md#daemon-configuration-file).  {{% include "daemon-cfg-desktop.md" %}}  The following example sets the log driver to `json-file` and sets the `max-size`  and `max-file` options to enable automatic log-rotation.  {  "log-driver": "json-file",  "log-opts": {  "max-size": "10m",  "max-file": "3"  }  }  > [!NOTE]  >  > `log-opts` co…
- `['Usage', 'Options']`
  > Options  The `json-file` logging driver supports the following logging options:  | Option         | Description                                                                                                                                                                                                   | Example value                                      |  | :------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | :----------…
- `['Usage', 'Options', 'Examples']`
  > Examples  This example starts an `alpine` container which can have a maximum of 3 log  files no larger than 10 megabytes each.  $ docker run -it --log-opt max-size=10m --log-opt max-file=3 alpine ash

`verdict_relevant:` ______   `notes:` ______

---

## 35. `13a7d6df9b8a0098`  (git)

**Query** (zh / code_api): git log 的自定义输出格式怎么写，比如只显示作者和提交时间？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/pretty-formats.adoc`

**Claimed section** (unverified): `['PRETTY FORMATS']`

**Actual sections in the index:**

- `[]`
  > PRETTY FORMATS  --------------  If the commit is a merge, and if the pretty-format  is not `oneline`, `email` or `raw`, an additional line is  inserted before the `Author:` line. This line begins with  "Merge: " and the hashes of ancestral commits are printed,  separated by spaces. Note that the listed commits may not  necessarily be the list of the 'direct' parent commits if you  have limited your view of history: for example, if you are  only interested in changes related to a certain directory or  file.  There are several built-in formats, and you can define  additional formats by setting a…
- `[]`
  > +%cN+:: committer name (respecting .mailmap, see  linkgit:git-shortlog[1] or linkgit:git-blame[1])  +%ce+:: committer email  +%cE+:: committer email (respecting .mailmap, see  linkgit:git-shortlog[1] or linkgit:git-blame[1])  +%cl+:: committer email local-part (the part before the `@` sign)  +%cL+:: committer local-part (see +%cl+) respecting .mailmap, see  linkgit:git-shortlog[1] or linkgit:git-blame[1])  +%cd+:: committer date (format respects --date= option)  +%cD+:: committer date, RFC2822 style  +%cr+:: committer date, relative  +%ct+:: committer date, UNIX timestamp  +%ci+:: committer da…
- `[]`
  > equivalent to giving it with `=true`.  If you add a `+` (plus sign) after +%+ of a placeholder, a line-feed  is inserted immediately before the expansion if and only if the  placeholder expands to a non-empty string.  If you add a `-` (minus sign) after +%+ of a placeholder, all consecutive  line-feeds immediately preceding the expansion are deleted if and only if the  placeholder expands to an empty string.  If you add a `' '` (space) after +%+ of a placeholder, a space  is inserted immediately before the expansion if and only if the  placeholder expands to a non-empty string.  --  `tformat:`…

`verdict_relevant:` ______   `notes:` ______

---

## 36. `49c85ee2d3a66b57`  (kubernetes)

**Query** (zh / config): 怎么配置集群的审计日志输出？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/services-networking/network-policies.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > If you want to control traffic flow at the IP address or port level for TCP, UDP, and SCTP protocols,  then you might consider using Kubernetes NetworkPolicies for particular applications in your cluster.  NetworkPolicies are an application-centric construct which allow you to specify how a  {{< glossary_tooltip text="pod" term_id="pod">}} is allowed to communicate with various network  "entities" (we use the word "entity" here to avoid overloading the more common terms such as  "endpoints" and "services", which have specific Kubernetes connotations) over the network.  NetworkPolicies apply to…
- `['Prerequisites']`
  > Prerequisites  Network policies are implemented by the [network plugin](/docs/concepts/extend-kubernetes/compute-storage-net/network-plugins/).  To use network policies, you must be using a networking solution which supports NetworkPolicy.  Creating a NetworkPolicy resource without a controller that implements it will have no effect.
- `['Prerequisites', 'The two sorts of pod isolation']`
  > The two sorts of pod isolation  There are two sorts of isolation for a pod: isolation for egress, and isolation for ingress.  They concern what connections may be established. "Isolation" here is not absolute, rather it  means "some restrictions apply". The alternative, "non-isolated for $direction", means that no  restrictions apply in the stated direction. The two sorts of isolation (or not) are declared  independently, and are both relevant for a connection from one pod to another.  By default, a pod is non-isolated for egress; all outbound connections are allowed.  A pod is isolated for eg…
- `['Prerequisites', 'The NetworkPolicy resource {#networkpolicy-resource}']`
  > The NetworkPolicy resource {#networkpolicy-resource}  See the [NetworkPolicy](/docs/reference/generated/kubernetes-api/{{< param "version" >}}/#networkpolicy-v1-networking-k8s-io)  reference for a full definition of the resource.  An example NetworkPolicy might look like this:  {{% code_sample file="service/networking/networkpolicy.yaml" %}}  {{< note >}}  POSTing this to the API server for your cluster will have no effect unless your chosen networking  solution supports network policy.  {{< /note >}}  __Mandatory Fields__: As with all other Kubernetes config, a NetworkPolicy needs `apiVersion…
- `['Prerequisites', 'Behavior of `to` and `from` selectors']`
  > Behavior of `to` and `from` selectors  There are four kinds of selectors that can be specified in an `ingress` `from` section or `egress`  `to` section:  **podSelector**: This selects particular Pods in the same namespace as the NetworkPolicy which  should be allowed as ingress sources or egress destinations.  **namespaceSelector**: This selects particular namespaces for which all Pods should be allowed as  ingress sources or egress destinations.  **namespaceSelector** *and* **podSelector**: A single `to`/`from` entry that specifies both  `namespaceSelector` and `podSelector` selects particula…
- `['Prerequisites', 'Default policies']`
  > Default policies  By default, if no policies exist in a namespace, then all ingress and egress traffic is allowed to  and from pods in that namespace. The following examples let you change the default behavior  in that namespace.

`verdict_relevant:` ______   `notes:` ______

---

## 37. `217622926d2a6b73`  (python)

**Query** (zh / code_api): 怎么用 dataclasses 定义数据类，让字段带默认值或默认工厂？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/dataclasses.rst`

**Claimed section** (unverified): `['Module contents', 'Default factory functions']`

**Actual sections in the index:**

- `[]`
  > :mod:`!dataclasses` --- Data Classes  ====================================  .. module:: dataclasses  :synopsis: Generate special methods on user-defined classes.  **Source code:** :source:`Lib/dataclasses.py`  --------------  This module provides a decorator and functions for automatically  adding generated :term:`special methods <special method>` such as :meth:`~object.__init__` and  :meth:`~object.__repr__` to user-defined classes. It was originally described  in :pep:`557`.  The member variables to use in these generated methods are defined  using :pep:`526` type annotations. For example, t…
- `[]`
  > and *frozen* is true, then :exc:`TypeError` is raised.  - *match_args*: If true (the default is ``True``), the  :attr:`~object.__match_args__` tuple will be created from the list of  non keyword-only parameters to the generated :meth:`~object.__init__` method (even if  :meth:`!__init__` is not generated, see above). If false, or if  :attr:`!__match_args__` is already defined in the class, then  :attr:`!__match_args__` will not be generated.  .. versionadded:: 3.10  - *kw_only*: If true (the default value is ``False``), then all  fields will be marked as keyword-only. If a field is marked as  k…
- `[]`
  > :attr:`!C.t` will be ``20``, and the class attributes :attr:`!C.x` and  :attr:`!C.y` will not be set.  .. versionchanged:: 3.15  If *metadata* is ``None``, use an empty :class:`frozendict`, instead  of a :func:`~types.MappingProxyType` of an empty :class:`dict`.  .. class:: Field  :class:`!Field` objects describe each defined field. These objects  are created internally, and are returned by the :func:`fields`  module-level method (see below). Users should never instantiate a  :class:`!Field` object directly. Its documented attributes are:  - :attr:`!name`: The name of the field.  - :attr:`!typ…
- `[]`
  > keyword-only fields. Note that a pseudo-field of type  :const:`!KW_ONLY` is otherwise completely ignored. This includes the  name of such a field. By convention, a name of ``_`` is used for a  :const:`!KW_ONLY` field. Keyword-only fields signify  :meth:`~object.__init__` parameters that must be specified as keywords when  the class is instantiated.  In this example, the fields ``y`` and ``z`` will be marked as keyword-only fields::  @dataclass  class Point:  x: float  _: KW_ONLY  y: float  z: float  p = Point(0, y=1.5, z=2.0)  In a single dataclass, it is an error to specify more than one  fie…
- `[]`
  > Default factory functions  -------------------------  If a :func:`field` specifies a *default_factory*, it is called with  zero arguments when a default value for the field is needed. For  example, to create a new instance of a list, use::  mylist: list = field(default_factory=list)  If a field is excluded from :meth:`~object.__init__` (using ``init=False``)  and the field also specifies *default_factory*, then the default  factory function will always be called from the generated  :meth:`!__init__` function. This happens because there is no other  way to give the field an initial value.  Muta…

`verdict_relevant:` ______   `notes:` ______

---

## 38. `ecbc5341c5e63a89`  (docker)

**Query** (en / command): How do you read the Docker daemon logs to see what happened on the host?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/daemon/logs.md`

**Claimed section** (unverified): `['Read the daemon logs']`

**Actual sections in the index:**

- `[]`
  > The daemon logs may help you diagnose problems. The logs may be saved in one of  a few locations, depending on the operating system configuration and the logging  subsystem used:  | Operating system             | Location                                                                                                                                 |  | :--------------------------- | :--------------------------------------------------------------------------------------------------------------------------------------- |  | Linux                        | Use the command `journalctl -xu docker.se…
- `['Enable debugging']`
  > Enable debugging  There are two ways to enable debugging. The recommended approach is to set the  `debug` key to `true` in the `daemon.json` file. This method works for every  Docker platform.  1.  Edit the `daemon.json` file, which is usually located in `/etc/docker/`. You  may need to create this file, if it doesn't yet exist. On macOS or Windows,  don't edit the file directly. Instead, edit the file through the Docker Desktop settings.  2.  If the file is empty, add the following:  {  "debug": true  }  If the file already contains JSON, just add the key `"debug": true`, being  careful to ad…
- `['Enable debugging', 'Force a stack trace to be logged']`
  > Force a stack trace to be logged  If the daemon is unresponsive, you can force a full stack trace to be logged by  sending a `SIGUSR1` signal to the daemon.  - **Linux**:  $ sudo kill -SIGUSR1 $(pidof dockerd)  - **Windows Server**:  Download [docker-signal](https://github.com/moby/docker-signal).  Get the process ID of dockerd `Get-Process dockerd`.  Run the executable with the flag `--pid=<PID of daemon>`.  This forces a stack trace to be logged but doesn't stop the daemon. Daemon logs  show the stack trace or the path to a file containing the stack trace if it was  logged to a file.  The da…
- `['Enable debugging', 'View stack traces']`
  > View stack traces  The Docker daemon log can be viewed by using one of the following methods:  - By running `journalctl -u docker.service` on Linux systems using `systemctl`  - `/var/log/messages`, `/var/log/daemon.log`, or `/var/log/docker.log` on older  Linux systems  > [!NOTE]  >  > It isn't possible to manually generate a stack trace on Docker Desktop for  > Mac or Docker Desktop for Windows. However, you can click the Docker taskbar  > icon and choose **Troubleshoot** to send information to Docker if you run into  > issues.  Look in the Docker logs for a message like the following:  ...go…

`verdict_relevant:` ______   `notes:` ______

---

## 39. `e03924d2b34b0564`  (docker)

**Query** (zh / troubleshooting): Docker 官方镜像的漏洞扫描或策略检查出问题，怎么排查？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/dhi/how-to/troubleshoot.md`

**Claimed section** (unverified): `['Troubleshooting']`

**Actual sections in the index:**

- `[]`
  > This page covers debugging techniques and common issues you may encounter while  migrating to or using Docker Hardened Images (DHIs).
- `['General debugging']`
  > General debugging  Docker Hardened Images prioritize minimalism and security, which means  they intentionally leave out many common debugging tools (like shells or package  managers). This makes direct troubleshooting difficult without introducing risk.  To address this, you can use [Docker  Debug](/reference/cli/docker/debug/), a secure workflow that  temporarily attaches an ephemeral debug container to a running service or image  without modifying the original image.  This section shows how to debug Docker Hardened Images locally during development.  With Docker Debug, you can also debug con…
- `['General debugging', 'Use Docker Debug']`
  > Use Docker Debug
- `['General debugging', 'Use Docker Debug', 'Step 1: Run a container from a Hardened Image']`
  > Step 1: Run a container from a Hardened Image  Start with a DHI-based container that simulates an issue:  $ docker run -d --name myapp dhi.io/python:3.13 python -c "import time; time.sleep(300)"  This container doesn't include a shell or tools like `ps`, `top`, or `cat`.  If you try:  $ docker exec -it myapp sh  You'll see:  exec: "sh": executable file not found in $PATH
- `['General debugging', 'Use Docker Debug', 'Step 1: Run a container from a Hardened Image', 'Step 2: Use Docker Debug to inspect the container']`
  > Step 2: Use Docker Debug to inspect the container  Use the `docker debug` command to attach a temporary, tool-rich debug container to the running instance.  $ docker debug myapp  From here, you can inspect running processes, network status, or mounted files.  For example, to check running processes:  $ ps aux  Type `exit` to leave the container when done.
- `['General debugging', 'Use Docker Debug', 'Alternative debugging approaches']`
  > Alternative debugging approaches  In addition to using Docker Debug, you can also use the following approaches for  debugging DHI containers.

`verdict_relevant:` ______   `notes:` ______

---

## 40. `312363bf9fe1c094`  (git)

**Query** (zh / command): 怎么用 git ls-files 查看当前索引（暂存区）里的文件列表？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-ls-files.adoc`

**Claimed section** (unverified): `['git-ls-files', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-ls-files(1)  ===============  NAME  ----  git-ls-files - Show information about files in the index and the working tree  SYNOPSIS  --------  [verse]  'git ls-files' [-z] [-t] [-v] [-f]  [-c|--cached] [-d|--deleted] [-o|--others] [-i|--ignored]  [-s|--stage] [-u|--unmerged] [-k|--killed] [-m|--modified]  [--resolve-undo]  [--directory [--no-empty-directory]] [--eol]  [--deduplicate]  [-x <pattern>|--exclude=<pattern>]  [-X <file>|--exclude-from=<file>]  [--exclude-per-directory=<file>]  [--exclude-standard]  [--error-unmatch] [--with-tree=<tree-ish>]  [--full-name] [--recurse-submodules]  […
- `[]`
  > and in the working tree ("w/<eolinfo>") are shown for regular files,  followed by the ("attr/<eolattr>").  --sparse::  If the index is sparse, show the sparse directories without expanding  to the contained files. Sparse directories will be shown with a  trailing slash, such as "x/" for a sparse directory "x".  --format=<format>::  A string that interpolates `%(fieldname)` from the result being shown.  It also interpolates `%%` to `%`, and `%xXX` where `XX` are hex digits  interpolates to character with hex code `XX`; for example `%x00`  interpolates to `\0` (NUL), `%x09` to `\t` (TAB) and %x0…

`verdict_relevant:` ______   `notes:` ______

---

## 41. `76be003230fec5dc`  (postgresql)

**Query** (en / config): How do you configure client authentication with pg_hba.conf?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/client-auth.sgml`

**Claimed section** (unverified): `['Client Authentication', 'The pg_hba.conf File']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/client-auth.sgml -->  <chapter id="client-authentication">  <title>Client Authentication</title>  <indexterm zone="client-authentication">  <primary>client authentication</primary>  </indexterm>  <para>  When a client application connects to the database server, it  specifies which <productname>PostgreSQL</productname> database user name it  wants to connect as, much the same way one logs into a Unix computer  as a particular user. Within the SQL environment the active database  user name determines access privileges to database objects &mdash; see  <xref linkend="user-manag"…
- `[]`
  > </para>  </note>  </listitem>  </varlistentry>  <varlistentry>  <term><literal>hostssl</literal></term>  <listitem>  <para>  This record matches connection attempts made using TCP/IP,  but only when the connection is made with <acronym>SSL</acronym>  encryption.  </para>  <para>  To make use of this option the server must be built with  <acronym>SSL</acronym> support. Furthermore,  <acronym>SSL</acronym> must be enabled  by setting the <xref linkend="guc-ssl"/> configuration parameter (see  <xref linkend="ssl-tcp"/> for more information).  Otherwise, the <literal>hostssl</literal> record is ig…
- `[]`
  > equal to the client's IP address. If both directions match,  then the entry is considered to match. (The host name that is  used in <filename>pg_hba.conf</filename> should be the one that  address-to-name resolution of the client's IP address returns,  otherwise the line won't be matched. Some host name databases  allow associating an IP address with multiple host names, but  the operating system will only return one host name when asked  to resolve an IP address.)  </para>  <para>  A host name specification that starts with a dot  (<literal>.</literal>) matches a suffix of the actual host  na…
- `[]`
  > In addition to the method-specific options listed below, there is a  method-independent authentication option <literal>clientcert</literal>, which  can be specified in any <literal>hostssl</literal> record.  This option can be set to <literal>verify-ca</literal> or  <literal>verify-full</literal>. Both options require the client  to present a valid (trusted) SSL certificate, while  <literal>verify-full</literal> additionally enforces that the  <literal>cn</literal> (Common Name) in the certificate matches  the username or an applicable mapping.  This behavior is similar to the <literal>cert</l…
- `['Allow any user on the local system to connect to any database with']`
  > Allow any user on the local system to connect to any database with
- `['any database user name using Unix-domain sockets (the default for local']`
  > any database user name using Unix-domain sockets (the default for local

`verdict_relevant:` ______   `notes:` ______

---

## 42. `38186c1ce061570f`  (docker)

**Query** (en / config): How do I set up an IPv6 network for my containers?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/storage/drivers/overlayfs-driver.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > OverlayFS is a union filesystem.  This page refers to the Linux kernel driver as `OverlayFS` and to the Docker  storage driver as `overlay2`.  > [!NOTE]  > Docker Engine 29.0 and later uses the  > [containerd image store](/manuals/engine/storage/containerd.md) by default.  > The `overlay2` driver is a legacy storage driver that is superseded by the  > `overlayfs` containerd snapshotter. For more information, see  > [Select a storage driver](/manuals/engine/storage/drivers/select-storage-driver.md).  > [!NOTE]  > For `fuse-overlayfs` driver, check [Rootless mode documentation](/manuals/engine/s…
- `['Prerequisites']`
  > Prerequisites  The `overlay2` driver is supported if you meet the following prerequisites:  - Version 4.0 or higher of the Linux kernel, or RHEL or CentOS using  version 3.10.0-514 of the kernel or higher.  - The `overlay2` driver is supported on `xfs` backing filesystems,  but only with `d_type=true` enabled.  Use `xfs_info` to verify that the `ftype` option is set to `1`. To format an  `xfs` filesystem correctly, use the flag `-n ftype=1`.  - Changing the storage driver makes existing containers and images inaccessible  on the local system. Use `docker save` to save any images you have built…
- `['Prerequisites', 'Configure Docker with the `overlay2` storage driver']`
  > Configure Docker with the `overlay2` storage driver  <a name="configure-docker-with-the-overlay-or-overlay2-storage-driver"></a>  Before following this procedure, you must first meet all the  [prerequisites](#prerequisites).  The following steps outline how to configure the `overlay2` storage driver.  1. Stop Docker.  $ sudo systemctl stop docker  2. Copy the contents of `/var/lib/docker` to a temporary location.  $ cp -au /var/lib/docker /var/lib/docker.bk  3. If you want to use a separate backing filesystem from the one used by  `/var/lib/`, format the filesystem and mount it into `/var/lib/…
- `['Prerequisites', 'How the `overlay2` driver works']`
  > How the `overlay2` driver works  OverlayFS layers two directories on a single Linux host and presents them as  a single directory. These directories are called layers, and the unification  process is referred to as a union mount. OverlayFS refers to the lower directory  as `lowerdir` and the upper directory as `upperdir`. The unified view is exposed  through its own directory called `merged`.  The `overlay2` driver natively supports up to 128 lower OverlayFS layers. This  capability provides better performance for layer-related Docker commands such  as `docker build` and `docker commit`, and c…
- `['Prerequisites', 'How the `overlay2` driver works', 'Image and container layers on-disk']`
  > Image and container layers on-disk  After downloading a five-layer image using `docker pull ubuntu`, you can see  six directories under `/var/lib/docker/overlay2`.  > [!WARNING]  >  > Don't directly manipulate any files or directories within  > `/var/lib/docker/`. These files and directories are managed by Docker.  $ ls -l /var/lib/docker/overlay2  total 24  drwx------ 5 root root 4096 Jun 20 07:36 223c2864175491657d238e2664251df13b63adb8d050924fd1bfcdb278b866f7  drwx------ 3 root root 4096 Jun 20 07:36 3a36935c9df35472229c57f4a27105a136f5e4dbef0f87905b2e506e494e348b  drwx------ 5 root root 40…
- `['Prerequisites', 'How the `overlay2` driver works', 'Image and container layers on-disk (legacy overlay driver)']`
  > Image and container layers on-disk (legacy overlay driver)  The following `docker pull` command shows a Docker host downloading a Docker  image comprising five layers.  $ docker pull ubuntu  Using default tag: latest  latest: Pulling from library/ubuntu  5ba4f30e5bea: Pull complete  9d7d19c9dc56: Pull complete  ac6ad7efd0f9: Pull complete  e7491a747824: Pull complete  a3ed95caeb02: Pull complete  Digest: sha256:46fb5d001b88ad904c5c732b086b596b92cfb4a4840a3abd0e35dbb6870585e4  Status: Downloaded newer image for ubuntu:latest

`verdict_relevant:` ______   `notes:` ______

---

## 43. `6d5f3746dfe918c9`  (docker)

**Query** (en / troubleshooting): My Docker Desktop is running out of disk space; how do I clean up old images and containers?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/desktop/use-desktop/volumes.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > The **Volumes** view in Docker Desktop lets you create, inspect, delete, clone, empty, export, and import [Docker volumes](/manuals/engine/storage/volumes.md). You can also browse files and folders in volumes and see which containers are using them.
- `['View your volumes']`
  > View your volumes  You can view the following information about your volumes:  - Name: The name of the volume.  - Status: Whether the volume is in-use by a container or not.  - Created: How long ago the volume was created.  - Size: The size of the volume.  - Scheduled exports: Whether a scheduled export is active or not.  By default, the **Volumes** view displays a list of all the volumes.  You can filter and sort volumes as well as modify which columns are displayed by  doing the following:  - Filter volumes by name: Use the **Search** field.  - Filter volumes by status: To the right of the s…
- `['View your volumes', 'Create a volume']`
  > Create a volume  You use the following steps to create an empty volume. Alternatively, if you  [start a container with a volume](/manuals/engine/storage/volumes.md#start-a-container-with-a-volume)  that doesn't yet exist, Docker creates the volume for you.  To create a volume:  1. In the **Volumes** view, select the **Create** button.  2. In the **New Volume** modal, specify a volume name, and then select  **Create**.  To use the volume with a container, see [Use volumes](/manuals/engine/storage/volumes.md#start-a-container-with-a-volume).
- `['View your volumes', 'Inspect a volume']`
  > Inspect a volume  To explore the details of a specific volume, select a volume from the list. This  opens the detailed view.  The **Container in-use** tab displays the name of the container using the  volume, the image name, the port number used by the container, and the target. A  target is a path inside a container that gives access to the files in the  volume.  The **Stored data** tab displays the files and folders in the volume and the  file size. To save a file or a folder, right-click on the file or folder to  display the options menu, select **Save as...**, and then specify a location t…
- `['View your volumes', 'Clone a volume']`
  > Clone a volume  Cloning a volume creates a new volume with a copy of all of the data from the  cloned volume. When cloning a volume used by one or more running containers, the  containers are temporarily stopped while Docker clones the data, and then  restarted when the cloning process is completed.  To clone a volume:  1. Sign in to Docker Desktop. You must be signed in to clone a volume.  2. In the **Volumes** view, select the **Clone** icon in the **Actions** column  for the volume you want to clone.  3. In the **Clone a volume** modal, specify a **Volume name**, and then select  **Clone**.
- `['View your volumes', 'Delete one or more volumes']`
  > Delete one or more volumes  Deleting a volume deletes the volume and all its data. When a container is using  a volume, you can't delete the volume, even if the container is stopped.  You must first stop and remove any containers  using the volume before you can delete the volume.  To delete a volume:  1. In the **Volumes** view, select **Delete** icon in the **Actions** column for  the volume you want to delete.  2. In the **Delete volume?** modal, select **Delete forever**.  To delete multiple volumes:  1. In the **Volumes** view, select the checkbox next to all the volumes you want  to dele…

`verdict_relevant:` ______   `notes:` ______

---

## 44. `2053cce68dd67bb5`  (kubernetes)

**Query** (en / concept): How does the horizontal pod autoscaler decide to scale a workload?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/workloads/controllers/job.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > A Job creates one or more Pods and will continue to retry execution of the Pods until a specified number of them successfully terminate.  As pods successfully complete, the Job tracks the successful completions. When a specified number  of successful completions is reached, the task (ie, Job) is complete. Deleting a Job will clean up  the Pods it created. Suspending a Job will delete its active Pods until the Job  is resumed again.  A simple case is to create one Job object in order to reliably run one Pod to completion.  The Job object will start a new Pod if the first Pod fails or is deleted…
- `['Running an example Job']`
  > Running an example Job  Here is an example Job config. It computes π to 2000 places and prints it out.  It takes around 10s to complete.  {{% code_sample file="controllers/job.yaml" %}}  You can run the example with this command:  kubectl apply -f https://kubernetes.io/examples/controllers/job.yaml  The output is similar to this:  job.batch/pi created  Check on the status of the Job with `kubectl`:  {{< tabs name="Check status of Job" >}}  {{< tab name="kubectl describe job pi" codelang="bash" >}}  Name:           pi  Namespace:      default  Selector:       batch.kubernetes.io/controller-uid=…
- `['Running an example Job', 'Writing a Job spec']`
  > Writing a Job spec  As with all other Kubernetes config, a Job needs `apiVersion`, `kind`, and `metadata` fields.  When the control plane creates new Pods for a Job, the `.metadata.name` of the  Job is part of the basis for naming those Pods. The name of a Job must be a valid  [DNS subdomain](/docs/concepts/overview/working-with-objects/names#dns-subdomain-names)  value, but this can produce unexpected results for the Pod hostnames. For best compatibility,  the name should follow the more restrictive rules for a  [DNS label](/docs/concepts/overview/working-with-objects/names#dns-label-names).…
- `['Running an example Job', 'Writing a Job spec', 'Job Labels']`
  > Job Labels  Job labels will have `batch.kubernetes.io/` prefix for `job-name` and `controller-uid`.
- `['Running an example Job', 'Writing a Job spec', 'Pod Template']`
  > Pod Template  The `.spec.template` is the only required field of the `.spec`.  The `.spec.template` is a [pod template](/docs/concepts/workloads/pods/#pod-templates).  It has exactly the same schema as a {{< glossary_tooltip text="Pod" term_id="pod" >}},  except it is nested and does not have an `apiVersion` or `kind`.  In addition to required fields for a Pod, a pod template in a Job must specify appropriate  labels (see [pod selector](#pod-selector)) and an appropriate restart policy.  Only a [`RestartPolicy`](/docs/concepts/workloads/pods/pod-lifecycle/#restart-policy)  equal to `Never` or…
- `['Running an example Job', 'Writing a Job spec', 'Pod selector']`
  > Pod selector  The `.spec.selector` field is optional. In almost all cases you should not specify it.  See section [specifying your own pod selector](#specifying-your-own-pod-selector).

`verdict_relevant:` ______   `notes:` ______

---

## 45. `1595b4ea282efeaa`  (go)

**Query** (en / concept): How can Go assembly code reference Go constants and interact with Go types?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", 'Interacting with Go types and constants']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 46. `ef5ef77128df0898`  (kubernetes)

**Query** (en / troubleshooting): A Secret was deleted by accident; how do I restore it from a backup?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/architecture/garbage-collection.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > {{<glossary_definition term_id="garbage-collection" length="short">}} This  allows the clean up of resources like the following:  * [Terminated pods](/docs/concepts/workloads/pods/pod-lifecycle/#pod-garbage-collection)  * [Completed Jobs](/docs/concepts/workloads/controllers/ttlafterfinished/)  * [Objects without owner references](#owners-dependents)  * [Unused containers and container images](#containers-images)  * [Dynamically provisioned PersistentVolumes with a StorageClass reclaim policy of Delete](/docs/concepts/storage/persistent-volumes/#delete)  * [Stale or expired CertificateSigningR…
- `['Owners and dependents {#owners-dependents}']`
  > Owners and dependents {#owners-dependents}  Many objects in Kubernetes link to each other through [*owner references*](/docs/concepts/overview/working-with-objects/owners-dependents/).  Owner references tell the control plane which objects are dependent on others.  Kubernetes uses owner references to give the control plane, and other API  clients, the opportunity to clean up related resources before deleting an  object. In most cases, Kubernetes manages owner references automatically.  Ownership is different from the [labels and selectors](/docs/concepts/overview/working-with-objects/labels/)…
- `['Owners and dependents {#owners-dependents}', 'Cascading deletion {#cascading-deletion}']`
  > Cascading deletion {#cascading-deletion}  Kubernetes checks for and deletes objects that no longer have owner  references, like the pods left behind when you delete a ReplicaSet. When you  delete an object, you can control whether Kubernetes deletes the object's  dependents automatically, in a process called *cascading deletion*. There are  two types of cascading deletion, as follows:  * Foreground cascading deletion  * Background cascading deletion  You can also control how and when garbage collection deletes resources that have  owner references using Kubernetes {{<glossary_tooltip text="fin…
- `['Owners and dependents {#owners-dependents}', 'Cascading deletion {#cascading-deletion}', 'Foreground cascading deletion {#foreground-deletion}']`
  > Foreground cascading deletion {#foreground-deletion}  In foreground cascading deletion, the owner object you're deleting first enters  a *deletion in progress* state. In this state, the following happens to the  owner object:  * The Kubernetes API server sets the object's `metadata.deletionTimestamp`  field to the time the object was marked for deletion.  * The Kubernetes API server also sets the `metadata.finalizers` field to  `foregroundDeletion`.  * The object remains visible through the Kubernetes API until the deletion  process is complete.  After the owner object enters the *deletion in…
- `['Owners and dependents {#owners-dependents}', 'Cascading deletion {#cascading-deletion}', 'Background cascading deletion {#background-deletion}']`
  > Background cascading deletion {#background-deletion}  In background cascading deletion, the Kubernetes API server deletes the owner  object immediately and the garbage collector controller (custom or default)  cleans up the dependent objects in the background.  If a finalizer exists, it ensures that objects are not deleted until all necessary clean-up tasks are completed.  By default, Kubernetes uses background cascading deletion unless  you manually use foreground deletion or choose to orphan the dependent objects.  See [Use background cascading deletion](/docs/tasks/administer-cluster/use-ca…
- `['Owners and dependents {#owners-dependents}', 'Cascading deletion {#cascading-deletion}', 'Orphaned dependents']`
  > Orphaned dependents  When Kubernetes deletes an owner object, the dependents left behind are called  *orphan* objects. By default, Kubernetes deletes dependent objects. To learn how  to override this behaviour, see [Delete owner objects and orphan dependents](/docs/tasks/administer-cluster/use-cascading-deletion/#set-orphan-deletion-policy).

`verdict_relevant:` ______   `notes:` ______

---

## 47. `10985109aed01ad8`  (postgresql)

**Query** (zh / concept): PostgreSQL 支持哪些索引类型，各自的适用场景是什么？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/indices.sgml`

**Claimed section** (unverified): `['Indexes', 'Index Types']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/indices.sgml -->  <chapter id="indexes">  <title>Indexes</title>  <indexterm zone="indexes">  <primary>index</primary>  </indexterm>  <para>  Indexes are a common way to enhance database performance. An index  allows the database server to find and retrieve specific rows much  faster than it could do without an index. But indexes also add  overhead to the database system as a whole, so they should be used  sensibly.  </para>  <sect1 id="indexes-intro">  <title>Introduction</title>  <para>  Suppose we have a table similar to this:  <programlisting>  CREATE TABLE test1 (  id in…
- `[]`
  > <emphasis>if</emphasis> the pattern is a constant and is anchored to  the beginning of the string &mdash; for example, <literal>col LIKE  'foo%'</literal> or <literal>col ~ '^foo'</literal>, but not  <literal>col LIKE '%bar'</literal>. However, if your database does not  use the C locale you will need to create the index with a special  operator class to support indexing of pattern-matching queries; see  <xref linkend="indexes-opclass"/> below. It is also possible to use  B-tree indexes for <literal>ILIKE</literal> and  <literal>~*</literal>, but only if the pattern starts with  non-alphabetic…
- `[]`
  > columns is independent of whether <literal>INCLUDE</literal> columns  can be added to the index. Indexes can have up to 32 columns,  including <literal>INCLUDE</literal> columns. (This limit can be  altered when building <productname>PostgreSQL</productname>; see the  file <filename>pg_config_manual.h</filename>.)  </para>  <para>  A multicolumn B-tree index can be used with query conditions that  involve any subset of the index's columns, but the index is most  efficient when there are constraints on the leading (leftmost) columns.  The exact rule is that equality constraints on leading colum…
- `[]`
  > sort. For a query that requires scanning a large fraction of the  table, an explicit sort is likely to be faster than using an index  because it requires  less disk I/O due to following a sequential access pattern. Indexes are  more useful when only a few rows need be fetched. An important  special case is <literal>ORDER BY</literal> in combination with  <literal>LIMIT</literal> <replaceable>n</replaceable>: an explicit sort will have to process  all the data to identify the first <replaceable>n</replaceable> rows, but if there is  an index matching the <literal>ORDER BY</literal>, the first <…
- `[]`
  > queries involving only <structfield>x</structfield>, the multicolumn index could be  used, though it would be larger and hence slower than an index on  <structfield>x</structfield> alone. The last alternative is to create all three  indexes, but this is probably only reasonable if the table is searched  much more often than it is updated and all three types of query are  common. If one of the types of query is much less common than the  others, you'd probably settle for creating just the two indexes that  best match the common types.  </para>  </sect1>  <sect1 id="indexes-unique">  <title>Uniq…
- `[]`
  > WHERE url = '/index.html' AND client_ip = inet '192.168.100.23';  </programlisting>  </para>  <para>  Observe that this kind of partial index requires that the common  values be predetermined, so such partial indexes are best used for  data distributions that do not change. Such indexes can be recreated  occasionally to adjust for new data distributions, but this adds  maintenance effort.  </para>  </example>  <para>  Another possible use for a partial index is to exclude values from the  index that the  typical query workload is not interested in; this is shown in <xref  linkend="indexes-part…

`verdict_relevant:` ______   `notes:` ______

---

## 48. `12eb7cabddf3bd1a`  (docker)

**Query** (zh / command): 怎么查看运行中容器的 CPU、内存等资源占用指标？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/containers/runmetrics.md`

**Claimed section** (unverified): `['Runtime metrics']`

**Actual sections in the index:**

- `['Docker stats']`
  > Docker stats  You can use the `docker stats` command to live stream a container's  runtime metrics. The command supports CPU, memory usage, memory limit,  and network IO metrics.  The following is a sample output from the `docker stats` command  $ docker stats redis1 redis2  CONTAINER           CPU %               MEM USAGE / LIMIT     MEM %               NET I/O             BLOCK I/O  redis1              0.07%               796 KB / 64 MB        1.21%               788 B / 648 B       3.568 MB / 512 KB  redis2              0.07%               2.746 MB / 64 MB      4.29%               1.266 KB…
- `['Docker stats', 'Control groups']`
  > Control groups  Linux Containers rely on [control groups](https://www.kernel.org/doc/Documentation/cgroup-v1/cgroups.txt)  which not only track groups of processes, but also expose metrics about  CPU, memory, and block I/O usage. You can access those metrics and  obtain network usage metrics as well. This is relevant for "pure" LXC  containers, as well as for Docker containers.  Control groups are exposed through a pseudo-filesystem. In modern distributions, you  should find this filesystem under `/sys/fs/cgroup`. Under that directory, you  see multiple sub-directories, called `devices`, `free…
- `['Docker stats', 'Control groups', 'Enumerate cgroups']`
  > Enumerate cgroups  The file layout of cgroups is significantly different between v1 and v2.  If `/sys/fs/cgroup/cgroup.controllers` is present on your system, you are using v2,  otherwise you are using v1.  Refer to the subsection that corresponds to your cgroup version.  cgroup v2 is used by default on the following distributions:  - Fedora (since 31)  - Debian GNU/Linux (since 11)  - Ubuntu (since 21.10)  > [!IMPORTANT]  >  > The detailed metrics examples later in this page describe the cgroup v1 file  > layout. On cgroup v2 hosts, use this page to find the container's cgroup  > directory an…
- `['Docker stats', 'Control groups', 'Enumerate cgroups', 'cgroup v1']`
  > cgroup v1  You can look into `/proc/cgroups` to see the different control group subsystems  known to the system, the hierarchy they belong to, and how many groups they contain.  You can also look at `/proc/<pid>/cgroup` to see which control groups a process  belongs to. The control group is shown as a path relative to the root of  the hierarchy mountpoint. `/` means the process hasn't been assigned to a  group, while `/lxc/pumpkin` indicates that the process is a member of a  container named `pumpkin`.
- `['Docker stats', 'Control groups', 'Enumerate cgroups', 'cgroup v2']`
  > cgroup v2  On cgroup v2 hosts, the content of `/proc/cgroups` isn't meaningful.  See `/sys/fs/cgroup/cgroup.controllers` to the available controllers.
- `['Docker stats', 'Control groups', 'Changing cgroup version']`
  > Changing cgroup version  Changing cgroup version requires rebooting the entire system.  On systemd-based systems, cgroup v2 can be enabled by adding `systemd.unified_cgroup_hierarchy=1`  to the kernel command line.  To revert the cgroup version to v1, you need to set `systemd.unified_cgroup_hierarchy=0` instead.  If `grubby` command is available on your system (e.g. on Fedora), the command line can be modified as follows:  $ sudo grubby --update-kernel=ALL --args="systemd.unified_cgroup_hierarchy=1"  If `grubby` command isn't available, edit the `GRUB_CMDLINE_LINUX` line in `/etc/default/grub`…

`verdict_relevant:` ______   `notes:` ______

---

## 49. `00f87f0a833eac67`  (go)

**Query** (en / config): Where is the history of GODEBUG settings introduced and removed in each Go release documented?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `['Go, Backwards Compatibility, and GODEBUG', 'GODEBUG History']`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 50. `7f1b1cd06fc815cc`  (docker)

**Query** (en / config): How do you configure the Docker daemon to use a proxy?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/daemon/proxy.md`

**Claimed section** (unverified): `['Daemon proxy configuration']`

**Actual sections in the index:**

- `[]`
  > <a name="httphttps-proxy"></a>  If your organization uses a proxy server to connect to the internet, you may  need to configure the Docker daemon to use the proxy server. The daemon uses  a proxy server to access images stored on Docker Hub and other registries,  and to reach other nodes in a Docker swarm.  This page describes how to configure a proxy for the Docker daemon. For  instructions on configuring proxy settings for the Docker CLI, see [Configure  Docker CLI to use a proxy server](/manuals/engine/cli/proxy.md).  > [!IMPORTANT]  > Proxy configurations specified in the `daemon.json` are…
- `['Daemon configuration']`
  > Daemon configuration  You may configure proxy behavior for the daemon in the `daemon.json` file,  or using CLI flags for the `--http-proxy` or `--https-proxy` flags for the  `dockerd` command. Configuration using `daemon.json` is recommended.  {  "proxies": {  "http-proxy": "http://proxy.example.com:3128",  "https-proxy": "http://proxy.example.com:3128",  "no-proxy": "*.test.example.com,.example.org,127.0.0.0/8"  }  }  After changing the configuration file, restart the daemon for the proxy configuration to take effect:  $ sudo systemctl restart docker
- `['Daemon configuration', 'Environment variables']`
  > Environment variables  The Docker daemon checks the following environment variables in its start-up  environment to configure HTTP or HTTPS proxy behavior:  - `HTTP_PROXY`  - `http_proxy`  - `HTTPS_PROXY`  - `https_proxy`  - `NO_PROXY`  - `no_proxy`
- `['Daemon configuration', 'Environment variables', 'systemd unit file']`
  > systemd unit file  If you're running the Docker daemon as a systemd service, you can create a  systemd drop-in file that sets the variables for the `docker` service.  > **Note for rootless mode**  >  > The location of systemd configuration files are different when running Docker  > in [rootless mode](/manuals/engine/security/rootless.md). When running in  > rootless mode, Docker is started as a user-mode systemd service, and uses  > files stored in each users' home directory in  > `~/.config/systemd/<user>/docker.service.d/`. In addition, `systemctl` must  > be executed without `sudo` and with…

`verdict_relevant:` ______   `notes:` ______

---

## 51. `2464b9e1e93c6178`  (go)

**Query** (en / code_api): Do atomic operations participate in the happens-before ordering of the Go memory model?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_mem.html`

**Claimed section** (unverified): `['The Go Memory Model', 'Synchronization', 'Atomic Values']`

**Actual sections in the index:**

- `[]`
  > Introduction  The Go memory model specifies the conditions under which  reads of a variable in one goroutine can be guaranteed to  observe values produced by writes to the same variable in a different goroutine.  Advice  Programs that modify data being simultaneously accessed by multiple goroutines  must serialize such access.  To serialize access, protect the data with channel operations or other synchronization primitives  such as those in the sync  and sync/atomic packages.  If you must read the rest of this document to understand the behavior of your program,  you are being too clever.  Do…
- `[]`
  > meaning it has no program executions with read-write or write-write data races,  can only have outcomes explained by some sequentially consistent interleaving  of the goroutine executions.  (The proof is the same as Section 7 of Boehm and Adve's paper cited above.)  This property is called DRF-SC.  The intent of the formal definition is to match  the DRF-SC guarantee provided to race-free programs  by other languages, including C, C++, Java, JavaScript, Rust, and Swift.  Certain Go language operations such as goroutine creation and memory allocation  act as synchronization operations.  The eff…
- `[]`
  > the capacity of the channel corresponds to the maximum number of simultaneous uses,  sending an item acquires the semaphore, and receiving an item releases  the semaphore.  This is a common idiom for limiting concurrency.  This program starts a goroutine for every entry in the work list, but the  goroutines coordinate using the limit channel to ensure  that at most three are running work functions at a time.  var limit = make(chan int, 3)  func main() {  for _, w := range work {  go func(w func()) {  limit <- 1  w()  <-limit  }(w)  }  select{}  }  Locks  The sync package implements two lock da…
- `[]`
  > it must not allow a single read to observe multiple values,  and it must not allow a single write to write multiple values.  All the following examples assume that `*p` and `*q` refer to  memory locations accessible to multiple goroutines.  Not introducing data races into race-free programs means not moving  writes out of conditional statements in which they appear.  For example, a compiler must not invert the conditional in this program:  *p = 1  if cond {  *p = 2  }  That is, the compiler must not rewrite the program into this one:  *p = 2  if !cond {  *p = 1  }  If cond is false and another…

`verdict_relevant:` ______   `notes:` ______

---

## 52. `ebe954b1f25f7687`  (go)

**Query** (zh / concept): Go 的 channel 类型有哪几种方向，收发操作分别是怎么阻塞的？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Types', 'Channel types']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 53. `3bf240ba9c9a9ec4`  (go)

**Query** (en / command): Which command merges the Go release-note fragments under doc/next into a single file?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/README.md`

**Claimed section** (unverified): `['Release Notes']`

**Actual sections in the index:**

- `['Release Notes']`
  > Release Notes  The `initial` and `next` subdirectories of this directory are for release notes.
- `['Release Notes', 'For developers']`
  > For developers  Release notes should be added to `next` by editing existing files or creating  new files. **Do not add RELNOTE=yes comments in CLs.** Instead, add a file to  the CL (or ask the author to do so).  At the end of the development cycle, the files will be merged by being  concatenated in sorted order by pathname. Files in the directory matching the  glob "*stdlib/*minor" are treated specially. They should be in subdirectories  corresponding to standard library package paths, and headings for those package  paths will be generated automatically.  Files in this repo's `api/next` direc…
- `['Release Notes', 'For the release team']`
  > For the release team  The `relnote` tool, at `golang.org/x/build/cmd/relnote`, operates on the files  in `doc/next`.  As a release cycle nears completion, run `relnote todo` to get a list of  unfinished release note work.  To prepare the release notes for a release, run `relnote generate`.  That will merge the `.md` files in `next` into a single file.  Atomically (as close to it as possible) add that file to `_content/doc` directory  of the website repository and remove the `doc/next` directory in this repository.  To begin the next release development cycle, populate the contents of `next`  w…

`verdict_relevant:` ______   `notes:` ______

---

## 54. `00e59ee253c08e73`  (go)

**Query** (en / config): What is the syntax for setting GODEBUG environment variables to toggle Go runtime behaviors?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `['Go, Backwards Compatibility, and GODEBUG', 'Introduction']`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 55. `fd10abba6fffc853`  (python)

**Query** (en / troubleshooting): Is there a source-code level debugger with breakpoints and single-stepping for Python?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/faq/programming.rst`

**Claimed section** (unverified): `['Programming FAQ', 'General questions']`

**Actual sections in the index:**

- `[]`
  > :tocdepth: 2  ===============  Programming FAQ  ===============  .. only:: html  .. contents::  General questions  =================  Is there a source code-level debugger with breakpoints and single-stepping?  ---------------------------------------------------------------------------  Yes.  Several debuggers for Python are described below, and the built-in function  :func:`breakpoint` allows you to drop into any of them.  The pdb module is a simple but adequate console-mode debugger for Python. It is  part of the standard Python library, and is :mod:`documented in the Library  Reference Manu…
- `[]`
  > Assume you use a for loop to define a few different lambdas (or even plain  functions), for example::  >>> squares = []  >>> for x in range(5):  ... squares.append(lambda: x**2)  This gives you a list that contains 5 lambdas that calculate ``x**2``. You  might expect that, when called, they would return, respectively, ``0``, ``1``,  ``4``, ``9``, and ``16``. However, when you actually try you will see that  they all return ``16``::  >>> squares[2]()  16  >>> squares[4]()  16  This happens because ``x`` is not local to the lambdas, but is defined in  the outer scope, and it is accessed when the…
- `[]`
  > and class instances can lead to confusion.  Because of this feature, it is good programming practice to not use mutable  objects as default values. Instead, use ``None`` as the default value and  inside the function, check if the parameter is ``None`` and create a new  list/dictionary/whatever if it is. For example, don't write::  def foo(mydict={}):  ...  but::  def foo(mydict=None):  if mydict is None:  mydict = {} # create a new dict for local namespace  This feature can be useful. When you have a function that's time-consuming to  compute, a common technique is to cache the parameters and…
- `['Callers can only provide two parameters and optionally pass _cache by keyword']`
  > Callers can only provide two parameters and optionally pass _cache by keyword  def expensive(arg1, arg2, *, _cache={}):  if (arg1, arg2) in _cache:  return _cache[(arg1, arg2)]
- `['Calculate the value']`
  > Calculate the value  result = ... expensive computation ...  _cache[(arg1, arg2)] = result # Store result in the cache  return result  You could use a global variable containing a dictionary instead of the default  value; it's a matter of taste.  How can I pass optional or keyword parameters from one function to another?  ---------------------------------------------------------------------------  Collect the arguments using the ``*`` and ``**`` specifiers in the function's  parameter list; this gives you the positional arguments as a tuple and the  keyword arguments as a dictionary. You can t…
- `['Calculate the value']`
  > ...  >>> def func4(args):  ... args.a = 'new-value' # args is a mutable Namespace  ... args.b = args.b + 1 # change object in-place  ...  >>> args = Namespace(a='old-value', b=99)  >>> func4(args)  >>> vars(args)  {'a': 'new-value', 'b': 100}  There's almost never a good reason to get this complicated.  Your best choice is to return a tuple containing the multiple results.  How do you make a higher order function in Python?  --------------------------------------------------  You have two choices: you can use nested scopes or you can use callable objects.  For example, suppose you wanted to de…

`verdict_relevant:` ______   `notes:` ______

---

## 56. `77165a16f2bf384a`  (kubernetes)

**Query** (zh / code_api): 怎么用 CustomResourceDefinition 定义自己的 API 资源？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions.md`

**Claimed section** (unverified): `['Extend the Kubernetes API with CustomResourceDefinitions']`

**Actual sections in the index:**

- `[]`
  > This page shows how to install a  [custom resource](/docs/concepts/extend-kubernetes/api-extension/custom-resources/)  into the Kubernetes API by creating a  [CustomResourceDefinition](/docs/reference/generated/kubernetes-api/{{< param "version" >}}/#customresourcedefinition-v1-apiextensions-k8s-io).
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}} {{< version-check >}}  If you are using an older version of Kubernetes that is still supported, switch to  the documentation for that version to see advice that is relevant for your cluster.
- `['{{% heading "prerequisites" %}}', 'Create a CustomResourceDefinition']`
  > Create a CustomResourceDefinition  When you create a new CustomResourceDefinition (CRD), the Kubernetes API Server  creates a new RESTful resource path for each version you specify. The custom  resource created from a CRD object can be either namespaced or cluster-scoped,  as specified in the CRD's `spec.scope` field. As with existing built-in  objects, deleting a namespace deletes all custom objects in that namespace.  CustomResourceDefinitions themselves are non-namespaced and are available to  all namespaces.  For example, if you save the following CustomResourceDefinition to `resourcedefin…
- `['name must match the spec fields below, and be in the form: <plural>.<group>']`
  > name must match the spec fields below, and be in the form: <plural>.<group>  name: crontabs.stable.example.com  spec:
- `['group name to use for REST API: /apis/<group>/<version>']`
  > group name to use for REST API: /apis/<group>/<version>  group: stable.example.com
- `['list of versions supported by this CustomResourceDefinition']`
  > list of versions supported by this CustomResourceDefinition  versions:  - name: v1

`verdict_relevant:` ______   `notes:` ______

---

## 57. `4e168af3074ebf97`  (go)

**Query** (zh / troubleshooting): goroutine 执行结束后，它对内存的写操作在什么条件下一定对其他 goroutine 可见？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_mem.html`

**Claimed section** (unverified): `['The Go Memory Model', 'Goroutine destruction']`

**Actual sections in the index:**

- `[]`
  > Introduction  The Go memory model specifies the conditions under which  reads of a variable in one goroutine can be guaranteed to  observe values produced by writes to the same variable in a different goroutine.  Advice  Programs that modify data being simultaneously accessed by multiple goroutines  must serialize such access.  To serialize access, protect the data with channel operations or other synchronization primitives  such as those in the sync  and sync/atomic packages.  If you must read the rest of this document to understand the behavior of your program,  you are being too clever.  Do…
- `[]`
  > meaning it has no program executions with read-write or write-write data races,  can only have outcomes explained by some sequentially consistent interleaving  of the goroutine executions.  (The proof is the same as Section 7 of Boehm and Adve's paper cited above.)  This property is called DRF-SC.  The intent of the formal definition is to match  the DRF-SC guarantee provided to race-free programs  by other languages, including C, C++, Java, JavaScript, Rust, and Swift.  Certain Go language operations such as goroutine creation and memory allocation  act as synchronization operations.  The eff…
- `[]`
  > the capacity of the channel corresponds to the maximum number of simultaneous uses,  sending an item acquires the semaphore, and receiving an item releases  the semaphore.  This is a common idiom for limiting concurrency.  This program starts a goroutine for every entry in the work list, but the  goroutines coordinate using the limit channel to ensure  that at most three are running work functions at a time.  var limit = make(chan int, 3)  func main() {  for _, w := range work {  go func(w func()) {  limit <- 1  w()  <-limit  }(w)  }  select{}  }  Locks  The sync package implements two lock da…
- `[]`
  > it must not allow a single read to observe multiple values,  and it must not allow a single write to write multiple values.  All the following examples assume that `*p` and `*q` refer to  memory locations accessible to multiple goroutines.  Not introducing data races into race-free programs means not moving  writes out of conditional statements in which they appear.  For example, a compiler must not invert the conditional in this program:  *p = 1  if cond {  *p = 2  }  That is, the compiler must not rewrite the program into this one:  *p = 2  if !cond {  *p = 1  }  If cond is false and another…

`verdict_relevant:` ______   `notes:` ______

---

## 58. `1d278796456319f4`  (go)

**Query** (en / command): In Go assembly, how do you define initialized data and global symbols?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", 'Directives']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 59. `02d4c075925ee6a1`  (postgresql)

**Query** (en / config): How do you set up logical replication between PostgreSQL servers with publications?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/logical-replication.sgml`

**Claimed section** (unverified): `['Logical Replication', 'Publication']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/logical-replication.sgml -->  <chapter id="logical-replication">  <title>Logical Replication</title>  <para>  Logical replication is a method of replicating data objects and their  changes, based upon their replication identity (usually a primary key). We  use the term logical replication in contrast to physical replication, which uses exact  block addresses and byte-by-byte replication. PostgreSQL supports both  mechanisms concurrently, see <xref linkend="high-availability"/>. Logical  replication allows fine-grained control over both data replication and  security.  </para>…
- `[]`
  > A <firstterm>subscription</firstterm> is the downstream side of logical  replication. The node where a subscription is defined is referred to as  the <firstterm>subscriber</firstterm>. A subscription defines the connection  to another database and the set of publications (one or more) to which it  wants to subscribe.  </para>  <para>  The subscriber database behaves in the same way as any other PostgreSQL  instance and can be used as a publisher for other databases by defining its  own publications.  </para>  <para>  A subscriber node may have multiple subscriptions if desired. It is  possible…
- `[]`
  > Create the same tables on the subscriber.  <programlisting>  /* sub # */ CREATE TABLE t1(a int, b text, PRIMARY KEY(a));  /* sub # */ CREATE TABLE t2(c int, d text, PRIMARY KEY(c));  /* sub # */ CREATE TABLE t3(e int, f text, PRIMARY KEY(e));  </programlisting></para>  <para>  Insert data to the tables at the publisher side.  <programlisting>  /* pub # */ INSERT INTO t1 VALUES (1, 'one'), (2, 'two'), (3, 'three');  /* pub # */ INSERT INTO t2 VALUES (1, 'A'), (2, 'B'), (3, 'C');  /* pub # */ INSERT INTO t3 VALUES (1, 'i'), (2, 'ii'), (3, 'iii');  </programlisting></para>  <para>  Create publica…
- `[]`
  > /* sub - */ WITH (connect=false, slot_name='myslot');  WARNING: subscription was created, but is not connected  HINT: To initiate replication, you must manually create the replication slot, enable the subscription, and refresh the subscription.  </programlisting></para>  </listitem>  <listitem>  <para>  On the publisher, manually create a slot using the same name that was  specified during <literal>CREATE SUBSCRIPTION</literal>, e.g. "myslot".  <programlisting>  /* pub # */ SELECT * FROM pg_create_logical_replication_slot('myslot', 'pgoutput');  slot_name | lsn  -----------+------------  myslo…
- `[]`
  > for behavioral, security or performance reasons. If a published table sets a  row filter, a row is replicated only if its data satisfies the row filter  expression. This allows a set of tables to be partially replicated. The row  filter is defined per table. Use a <literal>WHERE</literal> clause after the  table name for each published table that requires data to be filtered out.  The <literal>WHERE</literal> clause must be enclosed by parentheses. See  <xref linkend="sql-createpublication"/> for details.  </para>  <sect2 id="logical-replication-row-filter-rules">  <title>Row Filter Rules</tit…
- `[]`
  > postgres | f | f | t | t | t | t | none | f  Tables:  "public.t1" WHERE ((a > 5) AND (c = 'NSW'::text))  Publication p2  Owner | All tables | All sequences | Inserts | Updates | Deletes | Truncates | Generated columns | Via root  ----------+------------+---------------+---------+---------+---------+-----------+-------------------+----------  postgres | f | f | t | t | t | t | none | f  Tables:  "public.t1"  "public.t2" WHERE (e = 99)  Publication p3  Owner | All tables | All sequences | Inserts | Updates | Deletes | Truncates | Generated columns | Via root  ----------+------------+------------…

`verdict_relevant:` ______   `notes:` ______

---

## 60. `6ec2dd712895934c`  (git)

**Query** (zh / troubleshooting): git merge 遇到冲突时会怎样，怎么查看冲突文件？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-merge.adoc`

**Claimed section** (unverified): `['git-merge', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-merge(1)  ============  NAME  ----  git-merge - Join two or more development histories together  SYNOPSIS  --------  [synopsis]  git merge [-n] [--stat] [--compact-summary] [--no-commit] [--squash] [--[no-]edit]  [--no-verify] [-s <strategy>] [-X <strategy-option>] [-S[<keyid>]]  [--[no-]allow-unrelated-histories]  [--[no-]rerere-autoupdate] [-m <msg>] [-F <file>]  [--into-name <branch>] [<commit>...]  git merge (--continue | --abort | --quit)  DESCRIPTION  -----------  Incorporates changes from the named commits (since the time their  histories diverged from the current branch) into the c…
- `[]`
  > merged is committed, and your `HEAD`, index, and working tree are  updated to it. It is possible to have modifications in the working  tree as long as they do not overlap; the update will preserve them.  When it is not obvious how to reconcile the changes, the following  happens:  1. The `HEAD` pointer stays the same.  2. The `MERGE_HEAD` ref is set to point to the other branch head.  3. Paths that merged cleanly are updated both in the index file and  in your working tree.  4. For conflicting paths, the index file records up to three  versions: stage 1 stores the version from the common ances…
- `[]`
  > `git merge --continue` to seal the deal. The latter command  checks whether there is a (interrupted) merge in progress  before calling `git commit`.  You can work through the conflict with a number of tools:  * Use a mergetool. `git mergetool` to launch a graphical  mergetool which will work through the merge with you.  * Look at the diffs. `git diff` will show a three-way diff,  highlighting changes from both the `HEAD` and `MERGE_HEAD`  versions. `git diff AUTO_MERGE` will show what changes you've  made so far to resolve textual conflicts.  * Look at the diffs from each branch. `git log --me…

`verdict_relevant:` ______   `notes:` ______

---

## 61. `16e48bb4731be437`  (go)

**Query** (zh / concept): Go 仓库里为新增 API 起草的发布说明文件应该放在哪个目录、按什么规则命名？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/README.md`

**Claimed section** (unverified): `['Release Notes']`

**Actual sections in the index:**

- `['Release Notes']`
  > Release Notes  The `initial` and `next` subdirectories of this directory are for release notes.
- `['Release Notes', 'For developers']`
  > For developers  Release notes should be added to `next` by editing existing files or creating  new files. **Do not add RELNOTE=yes comments in CLs.** Instead, add a file to  the CL (or ask the author to do so).  At the end of the development cycle, the files will be merged by being  concatenated in sorted order by pathname. Files in the directory matching the  glob "*stdlib/*minor" are treated specially. They should be in subdirectories  corresponding to standard library package paths, and headings for those package  paths will be generated automatically.  Files in this repo's `api/next` direc…
- `['Release Notes', 'For the release team']`
  > For the release team  The `relnote` tool, at `golang.org/x/build/cmd/relnote`, operates on the files  in `doc/next`.  As a release cycle nears completion, run `relnote todo` to get a list of  unfinished release note work.  To prepare the release notes for a release, run `relnote generate`.  That will merge the `.md` files in `next` into a single file.  Atomically (as close to it as possible) add that file to `_content/doc` directory  of the website repository and remove the `doc/next` directory in this repository.  To begin the next release development cycle, populate the contents of `next`  w…

`verdict_relevant:` ______   `notes:` ______

---

## 62. `fc86cf9b20a62019`  (python)

**Query** (zh / code_api): 怎么用 multiprocessing 在多个进程之间共享大量数据？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/threading.rst`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > :mod:`!threading` --- Thread-based parallelism  ==============================================  .. module:: threading  :synopsis: Thread-based parallelism.  **Source code:** :source:`Lib/threading.py`  --------------  This module constructs higher-level threading interfaces on top of the lower  level :mod:`_thread` module.  .. include:: ../includes/wasm-notavail.rst  Introduction  ------------  The :mod:`!threading` module provides a way to run multiple `threads  <https://en.wikipedia.org/wiki/Thread_(computing)>`_ (smaller  units of a process) concurrently within a single process. It allows f…
- `['Start threads for each link']`
  > Start threads for each link  threads = []  for link in links:
- `['Using `args` to pass positional arguments and `kwargs` for keyword arguments']`
  > Using `args` to pass positional arguments and `kwargs` for keyword arguments  t = threading.Thread(target=crawl, args=(link,), kwargs={"delay": 2})  threads.append(t)
- `['Start each thread']`
  > Start each thread  for t in threads:  t.start()
- `['Wait for all threads to finish']`
  > Wait for all threads to finish  for t in threads:  t.join()  .. versionchanged:: 3.7  This module used to be optional, it is now always available.  .. seealso::  :class:`concurrent.futures.ThreadPoolExecutor` offers a higher level interface  to push tasks to a background thread without blocking execution of the  calling thread, while still being able to retrieve their results when needed.  :mod:`queue` provides a thread-safe interface for exchanging data between  running threads.  :mod:`asyncio` offers an alternative approach to achieving task level  concurrency without requiring the use of mu…
- `['Wait for all threads to finish']`
  > .. function:: stack_size([size])  Return the thread stack size used when creating new threads. The optional  *size* argument specifies the stack size to be used for subsequently created  threads, and must be 0 (use platform or configured default) or a positive  integer value of at least 32,768 (32 KiB). If *size* is not specified,  0 is used. If changing the thread stack size is  unsupported, a :exc:`RuntimeError` is raised. If the specified stack size is  invalid, a :exc:`ValueError` is raised and the stack size is unmodified. 32 KiB  is currently the minimum supported stack size value to gua…

`verdict_relevant:` ______   `notes:` ______

---

## 63. `ce6296eb903703b9`  (docker)

**Query** (en / troubleshooting): What pitfalls should you watch for when running Docker in rootless mode?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/security/rootless/troubleshoot.md`

**Claimed section** (unverified): `['Troubleshooting']`

**Actual sections in the index:**

- `['Distribution-specific hint']`
  > Distribution-specific hint  {{< tabs >}}  {{< tab name="Ubuntu" >}}  - Ubuntu 24.04 and later enables restricted unprivileged user namespaces by  default, which prevents unprivileged processes in creating user namespaces  unless an AppArmor profile is configured to allow programs to use  unprivileged user namespaces.  If you install `docker-ce-rootless-extras` using the deb package (`apt-get  install docker-ce-rootless-extras`), then the AppArmor profile for  `rootlesskit` is already bundled with the `apparmor` deb package. With this  installation method, you don't need to add any manual the A…
- `['Distribution-specific hint', 'Known limitations']`
  > Known limitations  - Only the following storage drivers are supported:  - `overlay2` (only if running with kernel 5.11 or later)  - `fuse-overlayfs` (only if running with kernel 4.18 or later, and `fuse-overlayfs` is installed)  - `btrfs` (only if running with kernel 4.18 or later, or `~/.local/share/docker` is mounted with `user_subvol_rm_allowed` mount option)  - `vfs`  - cgroup is supported only when running with cgroup v2 and systemd. See [Limiting resources](./tips.md#limiting-resources).  - Following features are not supported:  - AppArmor  - Checkpoint  - Overlay network  - Exposing SCT…
- `['Distribution-specific hint', 'Known limitations', 'Historical limitations']`
  > Historical limitations
- `['Distribution-specific hint', 'Known limitations', 'Historical limitations', 'Until Docker Engine v29.5']`
  > Until Docker Engine v29.5  - Host network (`docker run --net=host`) was namespaced inside RootlessKit.  This meant that ports listened by containers with `--net=host` were not reachable from the real host network namespace.
- `['Distribution-specific hint', 'Troubleshooting']`
  > Troubleshooting
- `['Distribution-specific hint', 'Troubleshooting', 'Unable to install with systemd when systemd is present on the system']`
  > Unable to install with systemd when systemd is present on the system  $ dockerd-rootless-setuptool.sh install  [INFO] systemd not detected, dockerd-rootless.sh needs to be started manually:  ...  `rootlesskit` cannot detect systemd properly if you switch to your user via `sudo su`. For users which cannot be logged-in, you must use the `machinectl` command which is part of the `systemd-container` package. After installing `systemd-container` switch to `myuser` with the following command:  $ sudo machinectl shell myuser@  Where `myuser@` is your desired username and @ signifies this machine.

`verdict_relevant:` ______   `notes:` ______

---

## 64. `466261b52d5d028d`  (python)

**Query** (zh / code_api): 怎么用 re 模块写正则表达式来匹配和替换字符串？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/re.rst`

**Claimed section** (unverified): `['Regular expression syntax']`

**Actual sections in the index:**

- `[]`
  > :mod:`!re` --- Regular expression operations  ============================================  .. module:: re  :synopsis: Regular expression operations.  **Source code:** :source:`Lib/re/`  --------------  This module provides regular expression matching operations similar to  those found in Perl.  Both patterns and strings to be searched can be Unicode strings (:class:`str`)  as well as 8-bit strings (:class:`bytes`).  However, Unicode strings and 8-bit strings cannot be mixed:  that is, you cannot match a Unicode string with a bytes pattern or  vice-versa; similarly, when asking for a substitut…
- `[]`
  > string, and not just ``'<a>'``. Adding ``?`` after the quantifier makes it  perform the match in :dfn:`non-greedy` or :dfn:`minimal` fashion; as *few*  characters as possible will be matched. Using the RE ``<.*?>`` will match  only ``'<a>'``.  .. index::  single: *+; in regular expressions  single: ++; in regular expressions  single: ?+; in regular expressions  ``*+``, ``++``, ``?+``  Like the ``'*'``, ``'+'``, and ``'?'`` quantifiers, those where ``'+'`` is  appended also match as many times as possible.  However, unlike the true greedy quantifiers, these do not allow  back-tracking when the…
- `[]`
  > two sets may be combined with a set operator, as in `Unicode Technical  Standard #18`_:  * ``[A--B]`` (*difference*) matches a character that is in *A* but not  in *B*; for example ``[a-z--[aeiou]]`` matches an ASCII lowercase  consonant.  * ``[A&&B]`` (*intersection*) matches a character that is in both *A*  and *B*; for example ``[\w&&[a-z]]`` matches an ASCII lowercase letter.  * ``[A||B]`` (*union*) matches a character that is in *A* or in *B*; this  is the same as listing the members of both sets in a single set, but  allows combining nested sets.  Operators have no precedence and are app…
- `[]`
  > Python identifiers, and in :class:`bytes` patterns they can only contain  bytes in the ASCII range. Each group name must be defined only once within  a regular expression. A symbolic group is also a numbered group, just as if  the group were not named.  Named groups can be referenced in three contexts. If the pattern is  ``(?P<quote>['"]).*?(?P=quote)`` (i.e. matching a string quoted with either  single or double quotes):  +---------------------------------------+----------------------------------+  | Context of reference to group "quote" | Ways to reference it |  +============================…
- `[]`
  > Matches any Unicode decimal digit  (that is, any character in Unicode character category `[Nd]`__).  This includes ``[0-9]``, and also many other digit characters.  Matches ``[0-9]`` if the :py:const:`~re.ASCII` flag is used.  __ https://www.unicode.org/versions/Unicode17.0.0/core-spec/chapter-4/#G124142  For 8-bit (bytes) patterns:  Matches any decimal digit in the ASCII character set;  this is equivalent to ``[0-9]``.  .. index:: single: \D; in regular expressions  ``\D``  Matches any character which is not a decimal digit.  This is the opposite of ``\d``.  Matches ``[^0-9]`` if the :py:cons…
- `[]`
  > .. data:: I  IGNORECASE  Perform case-insensitive matching;  expressions like ``[A-Z]`` will also match lowercase letters.  Full Unicode matching (such as ``Ã�`` matching ``Ã¼``)  also works unless the :py:const:`~re.ASCII` flag  is used to disable non-ASCII matches.  The current locale does not change the effect of this flag  unless the :py:const:`~re.LOCALE` flag is also used.  Corresponds to the inline flag ``(?i)``.  Note that when the Unicode patterns ``[a-z]`` or ``[A-Z]`` are used in  combination with the :const:`IGNORECASE` flag, they will match the 52 ASCII  letters and 4 additional n…

`verdict_relevant:` ______   `notes:` ______

---

## 65. `fcaedb0124152cd5`  (kubernetes)

**Query** (zh / concept): Kubernetes 的 Service 是干什么的，有哪几种类型？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/services-networking/service.md`

**Claimed section** (unverified): `['Service']`

**Actual sections in the index:**

- `[]`
  > {{< glossary_definition term_id="service" length="short" prepend="In Kubernetes, a Service is" >}}  A key aim of Services in Kubernetes is that you don't need to modify your existing  application to use an unfamiliar service discovery mechanism.  You can run code in Pods, whether this is a code designed for a cloud-native world, or  an older app you've containerized. You use a Service to make that set of Pods available  on the network so that clients can interact with it.  If you use a {{< glossary_tooltip term_id="deployment" >}} to run your app,  that Deployment can create and destroy Pods d…
- `['Services in Kubernetes']`
  > Services in Kubernetes  The Service API, part of Kubernetes, is an abstraction to help you expose groups of  Pods over a network. Each Service object defines a logical set of endpoints (usually  these endpoints are Pods) along with a policy about how to make those pods accessible.  For example, consider a stateless image-processing backend which is running with  3 replicas.  Those replicas are fungible&mdash;frontends do not care which backend  they use.  While the actual Pods that compose the backend set may change, the  frontend clients should not need to be aware of that, nor should they ne…
- `['Services in Kubernetes', 'Cloud-native service discovery']`
  > Cloud-native service discovery  If you're able to use Kubernetes APIs for service discovery in your application,  you can query the {{< glossary_tooltip text="API server" term_id="kube-apiserver" >}}  for matching EndpointSlices. Kubernetes updates the EndpointSlices for a Service  whenever the set of Pods in a Service changes.  For non-native applications, Kubernetes offers ways to place a network port or load  balancer in between your application and the backend Pods.  Either way, your workload can use these [service discovery](#discovering-services)  mechanisms to find the target it wants t…
- `['Services in Kubernetes', 'Defining a Service']`
  > Defining a Service  A Service is an {{< glossary_tooltip text="object" term_id="object" >}}  (the same way that a Pod or a ConfigMap is an object). You can create,  view or modify Service definitions using the Kubernetes API. Usually  you use a tool such as `kubectl` to make those API calls for you.  For example, suppose you have a set of Pods that each listen on TCP port 9376  and are labelled as `app.kubernetes.io/name=MyApp`. You can define a Service to  publish that TCP listener:  {{% code_sample file="service/simple-service.yaml" %}}  Applying this manifest creates a new Service named "my…
- `['Services in Kubernetes', 'Defining a Service', 'Port definitions {#field-spec-ports}']`
  > Port definitions {#field-spec-ports}  Port definitions in Pods have names, and you can reference these names in the  `targetPort` attribute of a Service. For example, we can bind the `targetPort`  of the Service to the Pod port in the following way:  apiVersion: v1  kind: Service  metadata:  name: nginx-service  spec:  selector:  app.kubernetes.io/name: proxy  ports:  - name: name-of-service-port  protocol: TCP  port: 80  targetPort: http-web-svc  ---  apiVersion: v1  kind: Pod  metadata:  name: nginx  labels:  app.kubernetes.io/name: proxy  spec:  containers:  - name: nginx  image: nginx:stab…
- `['Services in Kubernetes', 'Defining a Service', 'Services without selectors']`
  > Services without selectors  Services most commonly abstract access to Kubernetes Pods thanks to the selector,  but when used with a corresponding set of  {{<glossary_tooltip term_id="endpoint-slice" text="EndpointSlices">}}  objects and without a selector, the Service can abstract other kinds of backends,  including ones that run outside the cluster.  For example:  * You want to have an external database cluster in production, but in your  test environment you use your own databases.  * You want to point your Service to a Service in a different  {{< glossary_tooltip term_id="namespace" >}} or…

`verdict_relevant:` ______   `notes:` ______

---

## 66. `5e2f05e0790c6ac3`  (python)

**Query** (en / troubleshooting): When fetching a URL with urllib, how do you handle HTTP errors and connection failures?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/howto/urllib2.rst`

**Claimed section** (unverified): `['Introduction', 'Handling Exceptions']`

**Actual sections in the index:**

- `[]`
  > .. _urllib-howto:  ***********************************************************  HOWTO Fetch Internet Resources Using The urllib Package  ***********************************************************  Introduction  ============  .. sidebar:: Related Articles  You may also find useful the following article on fetching web resources  with Python:  * `Basic Authentication <https://web.archive.org/web/20201215133350/http://www.voidspace.org.uk/python/articles/authentication.shtml>`__  A tutorial on *Basic Authentication*, with examples in Python.  **urllib.request** is a Python module for fetching UR…
- `[]`
  > ``User-Agent`` header [#]_. When you create a Request object you can  pass a dictionary of headers in. The following example makes the same  request as above, but identifies itself as a version of Internet  Explorer [#]_. ::  import urllib.parse  import urllib.request  url = 'http://www.someserver.com/cgi-bin/register.cgi'  user_agent = 'Mozilla/5.0 (Windows NT 6.1; Win64; x64)'  values = {'name': 'Michael Foord',  'location': 'Northampton',  'language': 'Python' }  headers = {'User-Agent': user_agent}  data = urllib.parse.urlencode(values)  data = data.encode('ascii')  req = urllib.request.Re…
- `['everything is fine']`
  > everything is fine  .. note::  The ``except HTTPError`` *must* come first, otherwise ``except URLError``  will *also* catch an :exc:`~urllib.error.HTTPError`.  Number 2  ~~~~~~~~  ::  from urllib.request import Request, urlopen  from urllib.error import URLError  req = Request(someurl)  try:  response = urlopen(req)  except URLError as e:  if hasattr(e, 'reason'):  print('We failed to reach a server.')  print('Reason: ', e.reason)  elif hasattr(e, 'code'):  print('The server couldn\'t fulfill the request.')  print('Error code: ', e.code)  else:  everything is fine  info and geturl  ===========…
- `['create a password manager']`
  > create a password manager  password_mgr = urllib.request.HTTPPasswordMgrWithDefaultRealm()
- `['Add the username and password.']`
  > Add the username and password.
- `['If we knew the realm, we could use it instead of None.']`
  > If we knew the realm, we could use it instead of None.  top_level_url = "http://example.com/foo/"  password_mgr.add_password(None, top_level_url, username, password)  handler = urllib.request.HTTPBasicAuthHandler(password_mgr)

`verdict_relevant:` ______   `notes:` ______

---

## 67. `fb41ea7ed52ed08c`  (docker)

**Query** (zh / troubleshooting): 构建镜像时缓存总是失效、每次都全量构建，怎么优化缓存利用率？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/build/cache/optimize.md`

**Claimed section** (unverified): `['Optimize cache usage in builds']`

**Actual sections in the index:**

- `[]`
  > When building with Docker, a layer is reused from the build cache if the  instruction and the files it depends on hasn't changed since it was previously  built. Reusing layers from the cache speeds up the build process because Docker  doesn't have to rebuild the layer again.  Here are a few techniques you can use to optimize build caching and speed up  the build process:  - [Order your layers](#order-your-layers): Putting the commands in your  Dockerfile into a logical order can help you avoid unnecessary cache  invalidation.  - [Keep the context small](#keep-the-context-small): The context is…
- `['Order your layers']`
  > Order your layers  Putting the commands in your Dockerfile into a logical order is a great place  to start. Because a change causes a rebuild for steps that follow, try to make  expensive steps appear near the beginning of the Dockerfile. Steps that change  often should appear near the end of the Dockerfile, to avoid triggering  rebuilds of layers that haven't changed.  Consider the following example. A Dockerfile snippet that runs a JavaScript  build from the source files in the current directory:
- `['syntax=docker/dockerfile:1']`
  > syntax=docker/dockerfile:1  FROM node  WORKDIR /app  COPY . .          # Copy over all files in the current directory  RUN npm install   # Install dependencies  RUN npm build     # Run build  This Dockerfile is rather inefficient. Updating any file causes a reinstall of  all dependencies every time you build the Docker image even if the dependencies  didn't change since last time.  Instead, the `COPY` command can be split in two. First, copy over the package  management files (in this case, `package.json` and `yarn.lock`). Then, install  the dependencies. Finally, copy over the project source…
- `['syntax=docker/dockerfile:1', 'Keep the context small']`
  > Keep the context small  The easiest way to make sure your context doesn't include unnecessary files is  to create a `.dockerignore` file in the root of your build context. The  `.dockerignore` file works similarly to `.gitignore` files, and lets you  exclude files and directories from the build context.  Here's an example `.dockerignore` file that excludes the `node_modules`  directory, all files and directories that start with `tmp`:  node_modules  tmp*  Ignore-rules specified in the `.dockerignore` file apply to the entire build  context, including subdirectories. This means it's a rather co…
- `['syntax=docker/dockerfile:1', 'Use bind mounts']`
  > Use bind mounts  You might be familiar with bind mounts for when you run containers with `docker  run` or Docker Compose. Bind mounts let you mount a file or directory from the  host machine into a container.
- `['bind mount using the -v flag']`
  > bind mount using the -v flag  docker run -v $(pwd):/path/in/container image-name

`verdict_relevant:` ______   `notes:` ______

---

## 68. `e79113d705c3049d`  (kubernetes)

**Query** (zh / concept): Kubernetes 的节点（Node）是什么，节点上有哪些组件？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/architecture/nodes.md`

**Claimed section** (unverified): `['Nodes']`

**Actual sections in the index:**

- `[]`
  > Kubernetes runs your {{< glossary_tooltip text="workload" term_id="workload" >}}  by placing {{< glossary_tooltip text="containers" term_id="container" >}} into Pods to run on _Nodes_.  A node may be a virtual or physical machine, depending on the cluster. Each node  is managed by the  {{< glossary_tooltip text="control plane" term_id="control-plane" >}}  and contains the services necessary to run  {{< glossary_tooltip text="Pods" term_id="pod" >}}.  Typically you have several nodes in a cluster; in a learning or resource-limited  environment, you might have only one node.  The [components](/d…
- `['Management']`
  > Management  There are two main ways to have Nodes added to the  {{< glossary_tooltip text="API server" term_id="kube-apiserver" >}}:  1. The kubelet on a node self-registers to the control plane  2. You (or another human user) manually add a Node object  After you create a Node {{< glossary_tooltip text="object" term_id="object" >}},  or the kubelet on a node self-registers, the control plane checks whether the new Node object  is valid. For example, if you try to create a Node from the following JSON manifest:  {  "kind": "Node",  "apiVersion": "v1",  "metadata": {  "name": "10.240.79.157",…
- `['Management', 'Node name uniqueness']`
  > Node name uniqueness  The [name](/docs/concepts/overview/working-with-objects/names#names) identifies a Node. Two Nodes  cannot have the same name at the same time. Kubernetes also assumes that a resource with the same  name is the same object. In the case of a Node, it is implicitly assumed that an instance using the  same name will have the same state (e.g. network settings, root disk contents) and attributes like  node labels. This may lead to inconsistencies if an instance was modified without changing its name.  If the Node needs to be replaced or updated significantly, the existing Node…
- `['Management', 'Node name uniqueness', 'Self-registration of Nodes']`
  > Self-registration of Nodes  When the kubelet flag `--register-node` is true (the default), the kubelet will attempt to  register itself with the API server. This is the preferred pattern, used by most distros.  For self-registration, the kubelet is started with the following options:  - `--kubeconfig` - Path to credentials to authenticate itself to the API server.  - `--cloud-provider` - How to talk to a {{< glossary_tooltip text="cloud provider" term_id="cloud-provider" >}}  to read metadata about itself.  - `--register-node` - Automatically register with the API server.  - `--register-with-t…
- `['Management', 'Node name uniqueness', 'Manual Node administration']`
  > Manual Node administration  You can create and modify Node objects using  {{< glossary_tooltip text="kubectl" term_id="kubectl" >}}.  When you want to create Node objects manually, set the kubelet flag `--register-node=false`.  You can modify Node objects regardless of the setting of `--register-node`.  For example, you can set labels on an existing Node or mark it unschedulable.  You can set optional node role(s) for nodes by adding one or more `node-role.kubernetes.io/<role>: <role>` labels to the node where characters of `<role>`  are limited by the [syntax](/docs/concepts/overview/working-…
- `['Management', 'Node status']`
  > Node status  A Node's status contains the following information:  * [Addresses](/docs/reference/node/node-status/#addresses)  * [Conditions](/docs/reference/node/node-status/#condition)  * [Capacity and Allocatable](/docs/reference/node/node-status/#capacity)  * [Info](/docs/reference/node/node-status/#info)  You can use `kubectl` to view a Node's status and other details:  kubectl describe node <insert-node-name-here>  See [Node Status](/docs/reference/node/node-status/) for more details.

`verdict_relevant:` ______   `notes:` ______

---

## 69. `1357ea6192a954d9`  (python)

**Query** (zh / config): Python 的 UTF-8 模式是什么，怎么开启？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/os.rst`

**Claimed section** (unverified): `['Python UTF-8 Mode']`

**Actual sections in the index:**

- `[]`
  > :mod:`!os` --- Miscellaneous operating system interfaces  ========================================================  .. module:: os  :synopsis: Miscellaneous operating system interfaces.  **Source code:** :source:`Lib/os.py`  --------------  This module provides a portable way of using operating system dependent  functionality. If you just want to read or write a file see :func:`open`, if  you want to manipulate paths, see the :mod:`os.path` module, and if you want to  read all the lines in all the files on the command line see the :mod:`fileinput`  module. For creating temporary files and dire…
- `[]`
  > typically during Python startup as part of processing :file:`site.py`. Changes  to the environment made after this time are not reflected in :data:`os.environ`,  except for changes made by modifying :data:`os.environ` directly.  This mapping may be used to modify the environment as well as query the  environment. :func:`putenv` will be called automatically when the mapping  is modified.  On Unix, keys and values use :func:`sys.getfilesystemencoding` and  ``'surrogateescape'`` error handler. Use :data:`environb` if you would like  to use a different encoding.  On Windows, the keys are converted…
- `[]`
  > and may be modified by calls to :func:`setgroups` if suitably privileged.  If built with a deployment target greater than ``10.5``,  :func:`getgroups` returns the current group access list for the user  associated with the effective user id of the process; the group access  list may change over the lifetime of the process, it is not affected by  calls to :func:`setgroups`, and its length is not limited to 16. The  deployment target value can be obtained with  :func:`sysconfig.get_config_var('MACOSX_DEPLOYMENT_TARGET') <sysconfig.get_config_var>`.  .. function:: getlogin()  Return the name of t…
- `[]`
  > Call the system call :c:func:`!setpgrp` or ``setpgrp(0, 0)`` depending on  which version is implemented (if any). See the Unix manual for the semantics.  .. availability:: Unix, not WASI.  .. function:: setpgid(pid, pgrp, /)  Call the system call :c:func:`!setpgid` to set the process group id of the  process with id *pid* to the process group with id *pgrp*. See the Unix manual  for the semantics.  .. availability:: Unix, not WASI.  .. function:: setpriority(which, who, priority)  .. index:: single: process; scheduling priority  Set program scheduling priority. The value *which* is one of  :co…
- `[]`
  > The :meth:`~io.IOBase.fileno` method can be used to obtain the file descriptor  associated with a :term:`file object` when required. Note that using the file  descriptor directly will bypass the file object methods, ignoring aspects such  as internal buffering of data.  .. function:: close(fd)  Close file descriptor *fd*.  .. note::  This function is intended for low-level I/O and must be applied to a file  descriptor as returned by :func:`os.open` or :func:`pipe`. To close a "file  object" returned by the built-in function :func:`open` or by :func:`popen` or  :func:`fdopen`, use its :meth:`~i…
- `[]`
  > Get the blocking mode of the file descriptor: ``False`` if the  :data:`O_NONBLOCK` flag is set, ``True`` if the flag is cleared.  See also :func:`set_blocking` and :meth:`socket.socket.setblocking`.  .. availability:: Unix, Windows.  The function is limited on WASI, see :ref:`wasm-availability` for more  information.  On Windows, this function is limited to pipes.  .. versionadded:: 3.5  .. versionchanged:: 3.12  Added support for pipes on Windows.  .. function:: grantpt(fd, /)  Grant access to the slave pseudo-terminal device associated with the  master pseudo-terminal device to which the fil…

`verdict_relevant:` ______   `notes:` ______

---

## 70. `6035a7983c7e2849`  (postgresql)

**Query** (en / command): How do you insert rows into a table and return the modified data?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/dml.sgml`

**Claimed section** (unverified): `['Data Manipulation', 'Inserting Data']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/dml.sgml -->  <chapter id="dml">  <title>Data Manipulation</title>  <para>  The previous chapter discussed how to create tables and other  structures to hold your data. Now it is time to fill the tables  with data. This chapter covers how to insert, update, and delete  table data. The chapter  after this will finally explain how to extract your long-lost data  from the database.  </para>  <sect1 id="dml-insert">  <title>Inserting Data</title>  <indexterm zone="dml-insert">  <primary>inserting</primary>  </indexterm>  <indexterm zone="dml-insert">  <primary>INSERT</primary>  <…
- `[]`
  > this does not create any ambiguity. Of course, the  <literal>WHERE</literal> condition does  not have to be an equality test. Many other operators are  available (see <xref linkend="functions"/>). But the expression  needs to evaluate to a Boolean result.  </para>  <para>  You can update more than one column in an  <command>UPDATE</command> command by listing more than one  assignment in the <literal>SET</literal> clause. For example:  <programlisting>  UPDATE mytable SET a = 5, b = 3, c = 1 WHERE a &gt; 0;  </programlisting>  </para>  </sect1>  <sect1 id="dml-delete">  <title>Deleting Data</t…
- `[]`
  > Therefore there is nothing for Session 2 to update/delete: neither the  modified row nor the leftovers. The portion of history that Session 2  intended to change is not affected.  </para>  <figure id="temporal-isolation-figure">  <title>Temporal Isolation Example</title>  <mediaobject>  <imageobject>  <imagedata fileref="images/temporal-isolation.svg" format="SVG" width="35%"/>  </imageobject>  </mediaobject>  </figure>  <para>  To solve these problems, precede every temporal update/delete with a  <literal>SELECT FOR UPDATE</literal> matching the same criteria (including  the targeted portion…

`verdict_relevant:` ______   `notes:` ______

---

## 71. `823253475c5cf89a`  (git)

**Query** (zh / config): 怎么给 Git 配置钩子脚本，让它在提交等事件发生时自动执行？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/githooks.adoc`

**Claimed section** (unverified): `['githooks', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > githooks(5)  ===========  NAME  ----  githooks - Hooks used by Git  SYNOPSIS  --------  $GIT_DIR/hooks/* (or \`git config core.hooksPath`/*)  DESCRIPTION  -----------  Hooks are programs you can place in a hooks directory to trigger  actions at certain points in git's execution. Hooks that don't have  the executable bit set are ignored.  By default the hooks directory is `$GIT_DIR/hooks`, but that can be  changed via the `core.hooksPath` configuration variable (see  linkgit:git-config[1]).  Before Git invokes a hook, it changes its working directory to either  $GIT_DIR in a bare repository or…
- `[]`
  > The default 'commit-msg' hook, when enabled, detects duplicate  `Signed-off-by` trailers, and aborts the commit if one is found.  post-commit  ~~~~~~~~~~~  This hook is invoked by linkgit:git-commit[1]. It takes no parameters, and is  invoked after a commit is made.  This hook is meant primarily for notification, and cannot affect  the outcome of `git commit`.  pre-rebase  ~~~~~~~~~~  This hook is called by linkgit:git-rebase[1] and can be used to prevent a  branch from getting rebased. The hook may be called with one or  two parameters. The first parameter is the upstream from which  the seri…
- `[]`
  > descendant of the commit object named by the old object name.  That is, to enforce a "fast-forward only" policy.  It could also be used to log the old..new status. However, it  does not know the entire set of branches, so it would end up  firing one e-mail per ref when used naively, though. The  <<post-receive,'post-receive'>> hook is more suited to that.  In an environment that restricts the users' access only to git  commands over the wire, this hook can be used to implement access  control without relying on filesystem ownership and group  membership. See linkgit:git-shell[1] for how you mi…
- `['Version and features negotiation.']`
  > Version and features negotiation.  S: PKT-LINE(version=1\0push-options atomic...)  S: flush-pkt  H: PKT-LINE(version=1\0push-options...)  H: flush-pkt
- `['Send commands from server to the hook.']`
  > Send commands from server to the hook.  S: PKT-LINE(<old-oid> <new-oid> <ref>)  S: ... ...  S: flush-pkt
- `["Send push-options only if the 'push-options' feature is enabled."]`
  > Send push-options only if the 'push-options' feature is enabled.  S: PKT-LINE(push-option)  S: ... ...  S: flush-pkt

`verdict_relevant:` ______   `notes:` ______

---

## 72. `32fdf6f76813ebfb`  (docker)

**Query** (zh / troubleshooting): Docker Desktop 出问题的时候，官方推荐的排查步骤有哪些？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/desktop/troubleshoot-and-support/troubleshoot/topics.md`

**Claimed section** (unverified): `['Troubleshoot topics for Docker Desktop']`

**Actual sections in the index:**

- `[]`
  > > [!TIP]  >  > If you do not find a solution in troubleshooting, browse the GitHub repositories or [create a new issue](https://github.com/docker/desktop-feedback).
- `['Topics for all platforms']`
  > Topics for all platforms
- `['Topics for all platforms', 'Certificates not set up correctly']`
  > Certificates not set up correctly
- `['Topics for all platforms', 'Certificates not set up correctly', 'Error message']`
  > Error message  When attempting to pull from a registry using `docker run`, you may encounter the following error:  Error response from daemon: Get http://192.168.203.139:5858/v2/: malformed HTTP response "\x15\x03\x01\x00\x02\x02"  Additionally, logs from the registry may show:  2017/06/20 18:15:30 http: TLS handshake error from 192.168.203.139:52882: tls: client didn't provide a certificate  2017/06/20 18:15:30 http: TLS handshake error from 192.168.203.139:52883: tls: first record does not look like a TLS handshake
- `['Topics for all platforms', 'Certificates not set up correctly', 'Error message', 'Possible causes']`
  > Possible causes  - Docker Desktop ignores certificates listed under insecure registries.  - Client certificates are not sent to insecure registries, causing handshake failures.
- `['Topics for all platforms', 'Certificates not set up correctly', 'Error message', 'Solution']`
  > Solution  - Ensure that your registry is properly configured with valid SSL certificates.  - If your registry is self-signed, configure Docker to trust the certificate by adding it to Docker’s certificates directory (/etc/docker/certs.d/ on Linux).  - If the issue persists, check your Docker daemon configuration and enable TLS authentication.

`verdict_relevant:` ______   `notes:` ______

---

## 73. `877ce17965c43d73`  (kubernetes)

**Query** (en / config): How do you configure resource requests and limits for containers?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/configuration/manage-resources-containers.md`

**Claimed section** (unverified): `['Resource Management for Pods and Containers']`

**Actual sections in the index:**

- `[]`
  > When you specify a {{< glossary_tooltip term_id="pod" >}}, you can optionally specify how much of each resource a  {{< glossary_tooltip text="container" term_id="container" >}} needs. The most common resources to specify are CPU and memory  (RAM); there are others.  When you specify the resource _request_ for containers in a Pod, the  {{< glossary_tooltip text="kube-scheduler" term_id="kube-scheduler" >}} uses this information to decide which node to place the Pod on.  When you specify a resource _limit_ for a container, the {{< glossary_tooltip text="kubelet" term_id="kubelet" >}} enforces th…
- `['Requests and limits']`
  > Requests and limits  If the node where a Pod is running has enough of a resource available, it's possible (and  allowed) for a container to use more resource than its `request` for that resource specifies.  For example, if you set a `memory` request of 256 MiB for a container, and that container is in  a Pod scheduled to a Node with 8GiB of memory and no other Pods, then the container can try to use  more RAM.  Limits are a different story. Both `cpu` and `memory` limits are applied by the kubelet (and  {{< glossary_tooltip text="container runtime" term_id="container-runtime" >}}),  and are ul…
- `['Requests and limits', 'Resource types']`
  > Resource types  A *resource type* has a base unit and can be requested, limited, or both.  Kubernetes has the following built-in resource types:  | Resource type | Description | Base unit |  |---|---|---|  | `cpu` | Compute processing | cpu (core) |  | `memory` | RAM | Bytes |  | `ephemeral-storage` | [Local ephemeral storage](/docs/concepts/storage/ephemeral-storage/) | Bytes |  | `hugepages-<size>` | [Huge pages](#huge-pages) (Linux only) | Bytes |  Clusters can also provide  [extended resources](/docs/concepts/configuration/manage-resources-containers/#extended-resources)  (resources with a…
- `['Requests and limits', 'Resource types', 'Huge pages']`
  > Huge pages  For Linux workloads, you can specify _huge page_ resources.  Huge pages are a Linux-specific feature where the node kernel allocates blocks of memory  that are much larger than the default page size.  For example, on a system where the default page size is 4KiB, you could specify a limit,  `hugepages-2Mi: 80Mi`. If the container tries allocating over 40 2MiB huge pages (a  total of 80 MiB), that allocation fails.  {{< note >}}  You cannot overcommit `hugepages-*` resources.  This is different from the `memory` and `cpu` resources.  {{< /note >}}  CPU and memory are collectively ref…
- `['Requests and limits', 'Resource requests and limits of Pod and container']`
  > Resource requests and limits of Pod and container  For each container, you can specify resource limits and requests,  including the following:  * `spec.containers[].resources.limits.cpu`  * `spec.containers[].resources.limits.memory`  * `spec.containers[].resources.limits.ephemeral-storage`  * `spec.containers[].resources.limits.hugepages-<size>`  * `spec.containers[].resources.requests.cpu`  * `spec.containers[].resources.requests.memory`  * `spec.containers[].resources.requests.ephemeral-storage`  * `spec.containers[].resources.requests.hugepages-<size>`  Although you can only specify reques…
- `['Requests and limits', 'Pod-level resource specification']`
  > Pod-level resource specification  {{< feature-state feature_gate_name="PodLevelResources" >}}  Provided your cluster has the `PodLevelResources`  [feature gate](/docs/reference/command-line-tools-reference/feature-gates/) enabled,  you can specify resource requests and limits at  the Pod level. At the Pod level, Kubernetes {{< skew currentVersion >}}  only supports resource requests or limits for specific resource types: `cpu` and /  or `memory` and / or `hugepages`. With this feature, Kubernetes allows you to declare an overall resource  budget for the Pod, which is especially helpful when de…

`verdict_relevant:` ______   `notes:` ______

---

## 74. `7cf0dce9b6a03f6b`  (git)

**Query** (en / troubleshooting): A blob object in my repository is corrupted; how do I recover it?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/howto/recover-corrupted-blob-object.adoc`

**Claimed section** (unverified): `['How to recover a corrupted blob object']`

**Actual sections in the index:**

- `[]`
  > How to recover a corrupted blob object  ======================================  -----------------------------------------------------------  On Fri, 9 Nov 2007, Yossi Leybovich wrote:  >  > Did not help still the repository look for this object?  > Any one know how can I track this object and understand which file is it  -----------------------------------------------------------  So exactly *because* the SHA-1 hash is cryptographically secure, the hash  itself doesn't actually tell you anything, in order to fix a corrupt  object you basically have to find the "original source" for it.  The ea…

`verdict_relevant:` ______   `notes:` ______

---

## 75. `e199a47b7b13efda`  (python)

**Query** (en / code_api): How do you create and await tasks with asyncio, and how do you cancel a task safely?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/asyncio-task.rst`

**Claimed section** (unverified): `['Coroutines and tasks', 'Creating tasks']`

**Actual sections in the index:**

- `[]`
  > .. currentmodule:: asyncio  ====================  Coroutines and tasks  ====================  This section outlines high-level asyncio APIs to work with coroutines  and Tasks.  .. contents::  :depth: 1  :local:  .. _coroutine:  Coroutines  ==========  **Source code:** :source:`Lib/asyncio/coroutines.py`  ----------------------------------------------------  :term:`Coroutines <coroutine>` declared with the async/await syntax is the  preferred way of writing asyncio applications. For example, the following  snippet of code prints "hello", waits 1 second,  and then prints "world"::  >>> import as…
- `['Wait until both tasks are completed (should take']`
  > Wait until both tasks are completed (should take
- `['around 2 seconds.)']`
  > around 2 seconds.)  await task1  await task2  print(f"finished at {time.strftime('%X')}")  Note that expected output now shows that the snippet runs  1 second faster than before::  started at 17:14:32  hello  world  finished at 17:14:34  * The :class:`asyncio.TaskGroup` class provides a more modern  alternative to :func:`create_task`.  Using this API, the last example becomes::  async def main():  async with asyncio.TaskGroup() as tg:  task1 = tg.create_task(  say_after(1, 'hello'))  task2 = tg.create_task(  say_after(2, 'world'))  print(f"started at {time.strftime('%X')}")
- `['The await is implicit when the context manager exits.']`
  > The await is implicit when the context manager exits.  print(f"finished at {time.strftime('%X')}")  The timing and output should be the same as for the previous version.  .. versionadded:: 3.11  :class:`asyncio.TaskGroup`.  .. _asyncio-awaitables:  Awaitables  ==========  We say that an object is an **awaitable** object if it can be used  in an :keyword:`await` expression. Many asyncio APIs are designed to  accept awaitables.  There are three main types of *awaitable* objects:  **coroutines**, **Tasks**, and **Futures**.  .. rubric:: Coroutines  Python coroutines are *awaitables* and therefore…
- `['Nothing happens if we just call "nested()".']`
  > Nothing happens if we just call "nested()".
- `['A coroutine object is created but not awaited,']`
  > A coroutine object is created but not awaited,

`verdict_relevant:` ______   `notes:` ______

---

## 76. `276a1431b981bd5f`  (docker)

**Query** (zh / code_api): Compose SDK 怎么用编程方式管理 Compose 项目？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/compose-sdk.md`

**Claimed section** (unverified): `['Using the Compose SDK']`

**Actual sections in the index:**

- `[]`
  > {{< summary-bar feature_name="Compose SDK" >}}  The `docker/compose` package can be used as a Go library by third-party applications to programmatically manage  containerized applications defined in Compose files. This SDK provides a comprehensive API that lets you  integrate Compose functionality directly into your applications, allowing you to load, validate, and manage  multi-container environments without relying on the Compose CLI.  Whether you need to orchestrate containers as part of  a deployment pipeline, build custom management tools, or embed container orchestration into your applic…
- `['Set up the SDK']`
  > Set up the SDK  To get started, create an SDK instance using the `NewComposeService()` function, which initializes a service with the  necessary configuration to interact with the Docker daemon and manage Compose projects. This service instance provides  methods for all core Compose operations including creating, starting, stopping, and removing containers, as well as  loading and validating Compose files. The service handles the underlying Docker API interactions and resource  management, allowing you to focus on your application logic.
- `['Set up the SDK', 'Requirements']`
  > Requirements  Before using the SDK, make sure you're using a compatible version of the Docker CLI.  require (  github.com/docker/cli v28.5.2+incompatible  )  Docker CLI version 29.0.0 and later depends on the new `github.com/moby/moby` module, whereas Docker Compose v5 currently depends on `github.com/docker/docker`. This means you need to pin `docker/cli v28.5.2+incompatible` to ensure compatibility and avoid build errors.
- `['Set up the SDK', 'Requirements', 'Example usage']`
  > Example usage  Here's a basic example demonstrating how to load a Compose project and start the services:  package main  import (  "context"  "log"  "github.com/docker/cli/cli/command"  "github.com/docker/cli/cli/flags"  "github.com/docker/compose/v5/pkg/api"  "github.com/docker/compose/v5/pkg/compose"  )  func main() {  ctx := context.Background()  dockerCLI, err := command.NewDockerCli()  if err != nil {  log.Fatalf("Failed to create docker CLI: %v", err)  }  err = dockerCLI.Initialize(&flags.ClientOptions{})  if err != nil {  log.Fatalf("Failed to initialize docker CLI: %v", err)  }  // Cre…
- `['Set up the SDK', 'Customizing the SDK']`
  > Customizing the SDK  The `NewComposeService()` function accepts optional `compose.Option` parameters to customize the SDK behavior. These  options allow you to configure I/O streams, concurrency limits, dry-run mode, and other advanced features.  // Create a custom output buffer to capture logs  var outputBuffer bytes.Buffer  // Create a compose service with custom options  service, err := compose.NewComposeService(dockerCLI,  compose.WithOutputStream(&outputBuffer),          // Redirect output to custom writer  compose.WithErrorStream(os.Stderr),               // Use stderr for errors  compos…
- `['Set up the SDK', 'Customizing the SDK', 'Available options']`
  > Available options  - `WithOutputStream(io.Writer)`: Redirect standard output to a custom writer  - `WithErrorStream(io.Writer)`: Redirect error output to a custom writer  - `WithInputStream(io.Reader)`: Provide a custom input stream for interactive prompts  - `WithStreams(out, err, in)`: Set all I/O streams at once  - `WithMaxConcurrency(int)`: Limit the number of concurrent operations against the Docker API  - `WithPrompt(Prompt)`: Customize user confirmation behavior (use `AlwaysOkPrompt()` for non-interactive mode)  - `WithDryRun`: Run operations in dry-run mode without actually applying ch…

`verdict_relevant:` ______   `notes:` ______

---

## 77. `5e6e2c8c325205e4`  (postgresql)

**Query** (en / concept): How do I use the Bloom filter index type for large text columns?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/gist.sgml`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/gist.sgml -->  <sect1 id="gist">  <title>GiST Indexes</title>  <indexterm>  <primary>index</primary>  <secondary>GiST</secondary>  </indexterm>  <sect2 id="gist-intro">  <title>Introduction</title>  <para>  <acronym>GiST</acronym> stands for Generalized Search Tree. It is a  balanced, tree-structured access method, that acts as a base template in  which to implement arbitrary indexing schemes. B-trees, R-trees and many  other indexing schemes can be implemented in <acronym>GiST</acronym>.  </para>  <para>  One advantage of <acronym>GiST</acronym> is that it allows the develop…
- `[]`
  > if the operator class wishes to support ordered scans (nearest-neighbor  searches). The optional ninth method <function>fetch</function> is needed if the  operator class wishes to support index-only scans, except when the  <function>compress</function> method is omitted. The optional tenth method  <function>options</function> is needed if the operator class has  user-specified parameters.  The optional eleventh method <function>sortsupport</function> is used to  speed up building a <acronym>GiST</acronym> index.  The optional twelfth method <function>stratnum</function> is used to  translate c…
- `['ifdef NOT_USED']`
  > ifdef NOT_USED  Oid subtype = PG_GETARG_OID(3);
- `['endif']`
  > endif  bool *recheck = (bool *) PG_GETARG_POINTER(4);  data_type *key = DatumGetDataType(entry-&gt;key);  bool retval;  /*  * determine return value as a function of strategy, key and query.  *  * Use GIST_LEAF(entry) to know where you're called in the index tree,  * which comes handy when supporting the = operator for example (you could  * check for non empty union() in non-leaf nodes and equality in leaf  * nodes).  */  *recheck = true; /* or false if check is exact */  PG_RETURN_BOOL(retval);  }  </programlisting>  Here, <varname>key</varname> is an element in the index and <varname>query</…
- `['endif']`
  > Returns a value indicating the <quote>cost</quote> of inserting the new  entry into a particular branch of the tree. Items will be inserted  down the path of least <function>penalty</function> in the tree.  Values returned by <function>penalty</function> should be non-negative.  If a negative value is returned, it will be treated as zero.  </para>  <para>  The <acronym>SQL</acronym> declaration of the function must look like this:  <programlisting>  CREATE OR REPLACE FUNCTION my_penalty(internal, internal, internal)  RETURNS internal  AS 'MODULE_PATHNAME'  LANGUAGE C STRICT; -- in some cases p…
- `['ifdef NOT_USED']`
  > ifdef NOT_USED  Oid subtype = PG_GETARG_OID(3);  bool *recheck = (bool *) PG_GETARG_POINTER(4);

`verdict_relevant:` ______   `notes:` ______

---

## 78. `61b22f83424d2bbd`  (go)

**Query** (zh / concept): Go 里怎么启动一个 goroutine，go 语句的语义是什么？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Statements', 'Go statements']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 79. `ff10603dd1d78165`  (docker)

**Query** (zh / code_api): Bake 里怎么用变量来参数化构建定义？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/build/bake/variables.md`

**Claimed section** (unverified): `['Variables in Bake']`

**Actual sections in the index:**

- `[]`
  > You can define and use variables in a Bake file to set attribute values,  interpolate them into other values, and perform arithmetic operations.  Variables can be defined with default values, and can be overridden with  environment variables.
- `['Using variables as attribute values']`
  > Using variables as attribute values  Use the `variable` block to define a variable.  variable "TAG" {  default = "docker.io/username/webapp:latest"  }  The following example shows how to use the `TAG` variable in a target.  target "webapp" {  context = "."  dockerfile = "Dockerfile"  tags = [ TAG ]  }
- `['Using variables as attribute values', 'Interpolate variables into values']`
  > Interpolate variables into values  Bake supports string interpolation of variables into values. You can use the  `${}` syntax to interpolate a variable into a value. The following example  defines a `TAG` variable with a value of `latest`.  variable "TAG" {  default = "latest"  }  To interpolate the `TAG` variable into the value of an attribute, use the  `${TAG}` syntax.  group "default" {  targets = [ "webapp" ]  }  variable "TAG" {  default = "latest"  }  target "webapp" {  context = "."  dockerfile = "Dockerfile"  tags = ["docker.io/username/webapp:${TAG}"]  }  Printing the Bake file with t…
- `['Using variables as attribute values', 'Validating variables']`
  > Validating variables  To verify that the value of a variable conforms to an expected type, value  range, or other condition, you can define custom validation rules using the  `validation` block.  In the following example, validation is used to enforce a numeric constraint on  a variable value; the `PORT` variable must be 1024 or greater.
- `['Define a variable `PORT` with a default value and a validation rule']`
  > Define a variable `PORT` with a default value and a validation rule  variable "PORT" {  default = 3000  # Default value assigned to `PORT`
- `['Validation block to ensure `PORT` is a valid number within the acceptable range']`
  > Validation block to ensure `PORT` is a valid number within the acceptable range  validation {  condition = PORT >= 1024  # Ensure `PORT` is at least 1024  error_message = "The variable 'PORT' must be 1024 or greater."  # Error message for invalid values  }  }  If the `condition` expression evaluates to `false`, the variable value is  considered invalid, whereby the build invocation fails and `error_message` is  emitted. For example, if `PORT=443`, the condition evaluates to `false`, and  the error is raised.  Values are coerced into the expected type before the validation is set. This  ensures…

`verdict_relevant:` ______   `notes:` ______

---

## 80. `8b85db63ca7966cf`  (git)

**Query** (en / concept): What are the merge strategies in Git, and when is each one chosen automatically?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/merge-strategies.adoc`

**Claimed section** (unverified): `['MERGE STRATEGIES']`

**Actual sections in the index:**

- `[]`
  > MERGE STRATEGIES  ----------------  The merge mechanism (`git merge` and `git pull` commands) allows the  backend 'merge strategies' to be chosen with `-s` option. Some strategies  can also take their own options, which can be passed by giving `-X<option>`  arguments to `git merge` and/or `git pull`.  `ort`::  This is the default merge strategy when pulling or merging one  branch. This strategy can only resolve two heads using a  3-way merge algorithm. When there is more than one common  ancestor that can be used for 3-way merge, it creates a merged  tree of the common ancestors and uses that…

`verdict_relevant:` ______   `notes:` ______

---

## 81. `c1eafe828f5e9541`  (postgresql)

**Query** (en / code_api): How do you write a PL/pgSQL function with parameters and control structures?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/plpgsql.sgml`

**Claimed section** (unverified): `['PL/pgSQL — SQL Procedural Language', 'Structure of PL/pgSQL']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/plpgsql.sgml -->  <chapter id="plpgsql">  <title><application>PL/pgSQL</application> &mdash; <acronym>SQL</acronym> Procedural Language</title>  <indexterm zone="plpgsql">  <primary>PL/pgSQL</primary>  </indexterm>  <sect1 id="plpgsql-overview">  <title>Overview</title>  <para>  <application>PL/pgSQL</application> is a loadable procedural  language for the <productname>PostgreSQL</productname> database  system. The design goals of <application>PL/pgSQL</application> were to create  a loadable procedural language that  <itemizedlist>  <listitem>  <para>  can be used to create…
- `[]`
  > <literal>END</literal>, it must match the label at the block's beginning.  </para>  <para>  All key words are case-insensitive.  Identifiers are implicitly converted to lower case  unless double-quoted, just as they are in ordinary SQL commands.  </para>  <para>  Comments work the same way in <application>PL/pgSQL</application> code as in  ordinary SQL. A double dash (<literal>--</literal>) starts a comment  that extends to the end of the line. A <literal>/*</literal> starts a  block comment that extends to the matching occurrence of  <literal>*/</literal>. Block comments nest.  </para>  <para…
- `[]`
  > <literal>$<replaceable>n</replaceable></literal> names and optional  aliases in just the same way as the normal input parameters. An  output parameter is effectively a variable that starts out NULL;  it should be assigned to during the execution of the function.  The final value of the parameter is what is returned. For instance,  the sales-tax example could also be done this way:  <programlisting>  CREATE FUNCTION sales_tax(subtotal real, OUT tax real) AS $$  BEGIN  tax := subtotal * 0.06;  END;  $$ LANGUAGE plpgsql;  </programlisting>  Notice that we omitted <literal>RETURNS real</literal> &…
- `[]`
  > <literal>%TYPE</literal> is particularly valuable in polymorphic  functions, since the data types needed for internal variables can  change from one call to the next. Appropriate variables can be  created by applying <literal>%TYPE</literal> to the function's  arguments or result placeholders.  </para>  </sect2>  <sect2 id="plpgsql-declaration-rowtypes">  <title>Row Types</title>  <synopsis>  <replaceable>name</replaceable> <replaceable>table_name</replaceable><literal>%ROWTYPE</literal>;  <replaceable>name</replaceable> <replaceable>composite_type_name</replaceable>;  </synopsis>  <para>  A v…
- `[]`
  > really happens on first use of an expression is essentially a  <command>PREPARE</command> command. For example, if we have declared  two integer variables <literal>x</literal> and <literal>y</literal>, and we write  <programlisting>  IF x &lt; y THEN ...  </programlisting>  what happens behind the scenes is equivalent to  <programlisting>  PREPARE <replaceable>statement_name</replaceable>(integer, integer) AS SELECT $1 &lt; $2;  </programlisting>  and then this prepared statement is <command>EXECUTE</command>d for each  execution of the <command>IF</command> statement, with the current values…
- `[]`
  > and the plan is cached in the same way. Also, the special variable  <literal>FOUND</literal> is set to true if the query produced at  least one row, or false if it produced no rows (see  <xref linkend="plpgsql-statements-diagnostics"/>).  </para>  <note>  <para>  One might expect that writing <command>SELECT</command> directly  would accomplish this result, but at  present the only accepted way to do it is  <command>PERFORM</command>. An SQL command that can return rows,  such as <command>SELECT</command>, will be rejected as an error  unless it has an <literal>INTO</literal> clause as discuss…

`verdict_relevant:` ______   `notes:` ______

---

## 82. `2015b5792fc4ed37`  (python)

**Query** (zh / config): Python 源文件的默认编码是什么，怎么在文件里声明其他编码？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/interpreter.rst`

**Claimed section** (unverified): `['Using the Python Interpreter', 'Source Code Encoding']`

**Actual sections in the index:**

- `[]`
  > .. _tut-using:  ****************************  Using the Python Interpreter  ****************************  .. _tut-invoking:  Invoking the Interpreter  ========================  The Python interpreter is usually installed as |usr_local_bin_python_x_dot_y_literal|  on those machines where it is available; putting :file:`/usr/local/bin` in your  Unix shell's search path makes it possible to start it by typing the command:  .. code-block:: text  python3.16  to the shell. [#]_ Since the choice of the directory where the interpreter lives  is an installation option, other places are possible; check…
- `['-*- coding: encoding -*-']`
  > -*- coding: encoding -*-  where *encoding* is one of the valid :mod:`codecs` supported by Python.  For example, to declare that Windows-1252 encoding is to be used, the first  line of your source code file should be::
- `['-*- coding: cp1252 -*-']`
  > -*- coding: cp1252 -*-  One exception to the *first line* rule is when the source code starts with a  :ref:`UNIX "shebang" line <tut-scripts>`. In this case, the encoding  declaration should be added as the second line of the file. For example::
- `['!/usr/bin/env python3']`
  > !/usr/bin/env python3
- `['-*- coding: cp1252 -*-']`
  > -*- coding: cp1252 -*-  .. rubric:: Footnotes  .. [#] On Unix, the Python 3.x interpreter is by default not installed with the  executable named ``python``, so that it does not conflict with a  simultaneously installed Python 2.x executable.

`verdict_relevant:` ______   `notes:` ______

---

## 83. `8287909acc5ec3fe`  (postgresql)

**Query** (zh / command): 什么时候需要执行 VACUUM，它主要解决什么问题？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/maintenance.sgml`

**Claimed section** (unverified): `['Routine Database Maintenance Tasks', 'Routine Vacuuming']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/maintenance.sgml -->  <chapter id="maintenance">  <title>Routine Database Maintenance Tasks</title>  <indexterm zone="maintenance">  <primary>maintenance</primary>  </indexterm>  <indexterm zone="maintenance">  <primary>routine maintenance</primary>  </indexterm>  <para>  <productname>PostgreSQL</productname>, like any database software, requires that certain tasks  be performed regularly to achieve optimum performance. The tasks  discussed here are <emphasis>required</emphasis>, but they  are repetitive in nature and can easily be automated using standard  tools such as <app…
- `[]`
  > Some administrators prefer to schedule vacuuming themselves, for example  doing all the work at night when load is low.  The difficulty with doing vacuuming according to a fixed schedule  is that if a table has an unexpected spike in update activity, it may  get bloated to the point that <command>VACUUM FULL</command> is really necessary  to reclaim space. Using the autovacuum daemon alleviates this problem,  since the daemon schedules vacuuming dynamically in response to update  activity. It is unwise to disable the daemon completely unless you  have an extremely predictable workload. One pos…
- `[]`
  > <command>ANALYZE</command> commands on those tables on a suitable schedule.  </para>  </tip>  <tip>  <para>  The autovacuum daemon does not issue <command>ANALYZE</command> commands  for partitioned tables. Inheritance parents will only be analyzed if the  parent itself is changed - changes to child tables do not trigger  autoanalyze on the parent table. If your queries require statistics on  parent tables for proper planning, it is necessary to periodically run  a manual <command>ANALYZE</command> on those tables to keep the statistics  up to date.  </para>  </tip>  </sect2>  <sect2 id="vacuu…
- `[]`
  > any pages successfully eager frozen may be skipped during an aggressive  vacuum, so eager freezing may minimize the overhead of aggressive vacuums.  </para>  <para>  <xref linkend="guc-vacuum-freeze-table-age"/>  controls when a table is aggressively vacuumed. All all-visible but not all-frozen  pages are scanned if the number of transactions that have passed since the  last such scan is greater than <varname>vacuum_freeze_table_age</varname> minus  <varname>vacuum_freeze_min_age</varname>. Setting  <varname>vacuum_freeze_table_age</varname> to 0 forces <command>VACUUM</command> to  always use…
- `[]`
  > WARNING: database "mydb" must be vacuumed within 99985967 transactions  DETAIL: Approximately 4.66% of transaction IDs are available for use.  HINT: To avoid XID assignment failures, execute a database-wide VACUUM in that database.  </programlisting>  (A manual <command>VACUUM</command> should fix the problem, as suggested by the  hint; but note that the <command>VACUUM</command> should be performed by a  superuser, else it will fail to process system catalogs, which prevent it from  being able to advance the database's <structfield>datfrozenxid</structfield>.)  If these warnings are ignored,…
- `[]`
  > multixact allocation and usage patterns in real time, for example:  <screen>  =# SELECT *, pg_size_pretty(members_size) members_size_pretty  FROM pg_catalog.pg_get_multixact_stats();  num_mxids | num_members | members_size | oldest_multixact | members_size_pretty  -----------+-------------+--------------+------------------+---------------------  311740299 | 2785241176 | 13926205880 | 2 | 13 GB  (1 row)  </screen>  This output shows a system with significant multixact activity: about  312 million multixact IDs and about 2.8 billion member entries consuming  13 GB of storage space.  A spike in <…

`verdict_relevant:` ______   `notes:` ______

---

## 84. `c011823cab693cb0`  (python)

**Query** (en / command): How do I package my project as a wheel and publish it to PyPI with pip?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/zipapp.rst`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > :mod:`!zipapp` --- Manage executable Python zip archives  ========================================================  .. module:: zipapp  :synopsis: Manage executable Python zip archives  .. versionadded:: 3.5  **Source code:** :source:`Lib/zipapp.py`  .. index::  single: Executable Zip Files  --------------  This module provides tools to manage the creation of zip files containing  Python code, which can be :ref:`executed directly by the Python interpreter  <using-on-interface-options>`. The module provides both a  :ref:`zipapp-command-line-interface` and a :ref:`zipapp-python-api`.  Basic Exam…
- `[]`
  > archive from a directory, if the target is a file object it will be  passed to the ``zipfile.ZipFile`` class, and must supply the methods  needed by that class.  .. versionchanged:: 3.7  Added the *filter* and *compressed* parameters.  .. function:: get_interpreter(archive)  Return the interpreter specified in the ``#!`` line at the start of the  archive. If there is no ``#!`` line, return :const:`None`.  The *archive* argument can be a filename or a file-like object open  for reading in bytes mode. It is assumed to be at the start of the archive.  .. _zipapp-examples:  Examples  --------  Pac…
- `[]`
  > launcher on Windows. The interpreter should be encoded in UTF-8 on Windows,  and in :func:`sys.getfilesystemencoding` on POSIX.  2. Standard zipfile data, as generated by the :mod:`zipfile` module. The  zipfile content *must* include a file called ``__main__.py`` (which must be  in the "root" of the zipfile - i.e., it cannot be in a subdirectory). The  zipfile data can be compressed or uncompressed.  If an application archive has a shebang line, it may have the executable bit set  on POSIX systems, to allow it to be executed directly.  There is no requirement that the tools in this module are…

`verdict_relevant:` ______   `notes:` ______

---

## 85. `2c297550ad91d975`  (git)

**Query** (en / code_api): How do you query the attributes that apply to a path with git check-attr?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-check-attr.adoc`

**Claimed section** (unverified): `['git-check-attr', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-check-attr(1)  =================  NAME  ----  git-check-attr - Display gitattributes information  SYNOPSIS  --------  [verse]  'git check-attr' [--source <tree-ish>] [-a | --all | <attr>...] [--] <pathname>...  'git check-attr' --stdin [-z] [--source <tree-ish>] [-a | --all | <attr>...]  DESCRIPTION  -----------  For every pathname, this command will list if each attribute is 'unspecified',  'set', or 'unset' as a gitattribute on that pathname.  OPTIONS  -------  -a::  --all::  List all attributes that are associated with the specified  paths. If this option is used, then 'unspecified' att…

`verdict_relevant:` ______   `notes:` ______

---

## 86. `1d724fdae5787cd2`  (docker)

**Query** (zh / code_api): Syslog 日志驱动支持哪些配置项？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/logging/drivers/syslog.md`

**Claimed section** (unverified): `['Syslog logging driver']`

**Actual sections in the index:**

- `[]`
  > The `syslog` logging driver routes logs to a `syslog` server. The `syslog` protocol uses  a raw string as the log message and supports a limited set of metadata. The syslog  message must be formatted in a specific way to be valid. From a valid message, the  receiver can extract the following information:  - Priority: the logging level, such as `debug`, `warning`, `error`, `info`.  - Timestamp: when the event occurred.  - Hostname: where the event happened.  - Facility: which subsystem logged the message, such as `mail` or `kernel`.  - Process name and process ID (PID): The name and ID of the p…
- `['Usage']`
  > Usage  To use the `syslog` driver as the default logging driver, set the `log-driver`  and `log-opt` keys to appropriate values in the `daemon.json` file. For more  about configuring Docker using `daemon.json`, see  [daemon.json](/reference/cli/dockerd.md#daemon-configuration-file).  {{% include "daemon-cfg-desktop.md" %}}  The following example sets the log driver to `syslog` and sets the  `syslog-address` option. The `syslog-address` options supports both UDP and TCP;  this example uses UDP.  {  "log-driver": "syslog",  "log-opts": {  "syslog-address": "udp://1.2.3.4:1111"  }  }  Restart Doc…
- `['Usage', 'Options']`
  > Options  The following logging options are supported as options for the `syslog` logging  driver. They can be set as defaults in the `daemon.json`, by adding them as  key-value pairs to the `log-opts` JSON array. They can also be set on a given  container by adding a `--log-opt <key>=<value>` flag for each option when  starting the container.  | Option                   | Description…

`verdict_relevant:` ______   `notes:` ______

---

## 87. `e6cc7c227bff4a0f`  (docker)

**Query** (en / troubleshooting): Why does my build ignore the cache and rebuild everything?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/build/cache/invalidation.md`

**Claimed section** (unverified): `['Build cache invalidation']`

**Actual sections in the index:**

- `[]`
  > When building an image, Docker steps through the instructions in your  Dockerfile, executing each in the order specified. For each instruction, the  [builder](/manuals/build/builders/_index.md) checks whether it can reuse the  instruction from the build cache.
- `['General rules']`
  > General rules  The basic rules of build cache invalidation are as follows:  - The builder begins by checking if the base image is already cached. Each  subsequent instruction is compared against the cached layers. If no cached  layer matches the instruction exactly, the cache is invalidated.  - In most cases, comparing the Dockerfile instruction with the corresponding  cached layer is sufficient. However, some instructions require additional  checks and explanations.  - For the `ADD` and `COPY` instructions, and for `RUN` instructions with bind  mounts (`RUN --mount=type=bind`), the builder ca…
- `['General rules', 'WORKDIR and SOURCE_DATE_EPOCH']`
  > WORKDIR and SOURCE_DATE_EPOCH  The `WORKDIR` instruction respects the `SOURCE_DATE_EPOCH` build argument when  determining cache validity. Changing `SOURCE_DATE_EPOCH` between builds  invalidates the cache for `WORKDIR` and all subsequent instructions.  `SOURCE_DATE_EPOCH` sets timestamps for files created during the build. If you  set this to a dynamic value like a Git commit timestamp, the cache breaks with  each commit. This is expected behavior when tracking build provenance.  For reproducible builds without frequent cache invalidation, use a fixed  timestamp:  $ docker build --build-arg S…
- `['General rules', 'RUN instructions']`
  > RUN instructions  The cache for `RUN` instructions isn't invalidated automatically between builds.  Suppose you have a step in your Dockerfile to install `curl`:  FROM alpine:{{% param "example_alpine_version" %}} AS install  RUN apk add curl  This doesn't mean that the version of `curl` in your image is always up-to-date.  Rebuilding the image one week later will still get you the same packages as before.  To force a re-execution of the `RUN` instruction, you can:  - Make sure that a layer before it has changed  - Clear the build cache ahead of the build using  [`docker builder prune`](/refer…
- `['General rules', 'Build secrets']`
  > Build secrets  The contents of build secrets are not part of the build cache.  Changing the value of a secret doesn't result in cache invalidation.  If you want to force cache invalidation after changing a secret value,  you can pass a build argument with an arbitrary value that you also change when changing the secret.  Build arguments do result in cache invalidation.  FROM alpine  ARG CACHEBUST  RUN --mount=type=secret,id=TOKEN,env=TOKEN \  some-command ...  $ TOKEN="tkn_pat123456" docker build --secret id=TOKEN --build-arg CACHEBUST=1 .  Properties of secrets such as IDs and mount paths do…

`verdict_relevant:` ______   `notes:` ______

---

## 88. `686beb6a31cef67c`  (git)

**Query** (zh / concept): Git 的子模块是什么，为什么要用它来管理嵌套仓库？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/gitsubmodules.adoc`

**Claimed section** (unverified): `['Submodules']`

**Actual sections in the index:**

- `[]`
  > gitsubmodules(7)  ================  NAME  ----  gitsubmodules - Mounting one repository inside another  SYNOPSIS  --------  .gitmodules, $GIT_DIR/config  ------------------  git submodule  git <command> --recurse-submodules  ------------------  DESCRIPTION  -----------  A submodule is a repository embedded inside another repository.  The submodule has its own history; the repository it is embedded  in is called a superproject.  On the filesystem, a submodule usually (but not always - see FORMS below)  consists of (i) a Git directory located under the `$GIT_DIR/modules/`  directory of its super…
- `[]`
  > system, but the Git directory is kept around as it to make it  possible to checkout past commits without requiring fetching  from another repository.  +  To completely remove a submodule, manually delete  `$GIT_DIR/modules/<name>/`.  ACTIVE SUBMODULES  -----------------  A submodule is considered active,  1. if `submodule.<name>.active` is set to `true`  +  or  2. if the submodule's path matches the pathspec in `submodule.active`  +  or  3. if `submodule.<name>.url` is set.  and these are evaluated in this order.  For example:  [submodule "foo"]  active = false  url = https://example.org/foo…
- `['Add a submodule']`
  > Add a submodule  git submodule add <URL> <path>
- `['Occasionally update the submodule to a new version:']`
  > Occasionally update the submodule to a new version:  git -C <path> checkout <new-version>  git add <path>  git commit -m "update submodule to new version"
- `['See the list of submodules in a superproject']`
  > See the list of submodules in a superproject  git submodule status
- `['See FORMS on removing submodules']`
  > See FORMS on removing submodules  Workflow for an artificially split repo  ---------------------------------------

`verdict_relevant:` ______   `notes:` ______

---

## 89. `a5a4e5aac9945afa`  (git)

**Query** (zh / command): git commit 有哪些常用选项，比如修改最近一次提交信息？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-commit.adoc`

**Claimed section** (unverified): `['git-commit', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-commit(1)  =============  NAME  ----  git-commit - Record changes to the repository  SYNOPSIS  --------  [synopsis]  git commit [-a | --interactive | --patch] [-s] [-v] [-u[<mode>]] [--amend]  [--dry-run] [(-c | -C | --squash) <commit> | --fixup [(amend|reword):]<commit>]  [-F <file> | -m <msg>] [--reset-author] [--allow-empty]  [--allow-empty-message] [--no-verify] [-e] [--author=<author>]  [--date=<date>] [--cleanup=<mode>] [--[no-]status]  [-i | -o] [--pathspec-from-file=<file> [--pathspec-file-nul]]  [(--trailer <token>[(=|:)<value>])...] [-S[<keyid>]]  [--] [<pathspec>...]  DESCRIPTIO…
- `[]`
  > is assumed to be a pattern and is used to search for an existing  commit by that author (i.e. `git rev-list --all -i --author=<author>`);  the commit author is then copied from the first such commit found.  `--date=<date>`::  Override the author date used in the commit.  `-m <msg>`::  `--message=<msg>`::  Use _<msg>_ as the commit message.  If multiple `-m` options are given, their values are  concatenated as separate paragraphs.  +  The `-m` option is mutually exclusive with `-c`, `-C`, and `-F`.  `-t <file>`::  `--template=<file>`::  When editing the commit message, start the editor with the…
- `['------------------------ >8 ------------------------']`
  > ------------------------ >8 ------------------------  `default`::  Same as `strip` if the message is to be edited.  Otherwise `whitespace`.  --  +  The default can be changed by the `commit.cleanup` configuration  variable (see linkgit:git-config[1]).  `-e`::  `--edit`::  Let the user further edit the message taken from _<file>_  with `-F <file>`, command line with `-m <message>`, and  from _<commit>_ with `-C <commit>`.  `--no-edit`::  Use the selected commit message without launching an editor.  For example, `git commit --amend --no-edit` amends a commit  without changing its commit message.…
- `['------------------------ >8 ------------------------']`
  > your working tree and do corresponding `git add` and `git rm`  for you. That is, this example does the same as the earlier  example if there is no other change in your working tree:  ------------  $ edit hello.c  $ rm goodbye.c  $ git commit -a  ------------  The command `git commit -a` first looks at your working tree,  notices that you have modified `hello.c` and removed `goodbye.c`,  and performs necessary `git add` and `git rm` for you.  After staging changes to many files, you can alter the order the  changes are recorded in, by giving pathnames to `git commit`.  When pathnames are given,…

`verdict_relevant:` ______   `notes:` ______

---

## 90. `b0d555f872752403`  (python)

**Query** (zh / troubleshooting): Python 在 Windows 上启动特别慢是为什么，有什么解决办法？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/faq/windows.rst`

**Claimed section** (unverified): `['Python on Windows FAQ', 'Why does Python sometimes take so long to start?']`

**Actual sections in the index:**

- `[]`
  > :tocdepth: 2  .. highlight:: none  .. _windows-faq:  =====================  Python on Windows FAQ  =====================  .. only:: html  .. contents::  .. XXX need review for Python 3.  XXX need review for Windows Vista/Seven?  .. _faq-run-program-under-windows:  How do I run a Python program under Windows?  --------------------------------------------  This is not necessarily a straightforward question. If you are already familiar  with running programs from the Windows command line then everything will seem  obvious; otherwise, you might need a little more guidance.  Unless you use some sor…
- `[]`
  > In a .pyd, linkage is defined in a list of available functions.  How can I embed Python into a Windows application?  --------------------------------------------------  Embedding the Python interpreter in a Windows app can be summarized as follows:  1. Do **not** build Python into your .exe file directly. On Windows, Python must  be a DLL to handle importing modules that are themselves DLL's. (This is the  first key undocumented fact.) Instead, link to :file:`python{NN}.dll`; it is  typically installed in ``C:\Windows\System``. *NN* is the Python version, a  number such as "33" for Python 3.3.…
- `['include <Python.h>']`
  > include <Python.h>  ...  Py_Initialize(); // Initialize Python.  initmyAppc(); // Initialize (import) the helper class.  PyRun_SimpleString("import myApp"); // Import the shadow class.  5. There are two problems with Python's C API which will become apparent if you  use a compiler other than MSVC, the compiler used to build pythonNN.dll.  Problem 1: The so-called "Very High Level" functions that take ``FILE *``  arguments will not work in a multi-compiler environment because each  compiler's notion of a ``struct FILE`` will be different. From an implementation  standpoint these are very low le…

`verdict_relevant:` ______   `notes:` ______

---

## 91. `983b48d9cbb86bf0`  (kubernetes)

**Query** (zh / command): 怎么在 Linux 上安装并配置 kubectl？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/tools/install-kubectl-linux.md`

**Claimed section** (unverified): `['Install and Set Up kubectl on Linux']`

**Actual sections in the index:**

- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  You must use a kubectl version that is within one minor version difference of  your cluster. For example, a v{{< skew currentVersion >}} client can communicate  with v{{< skew currentVersionAddMinor -1 >}}, v{{< skew currentVersionAddMinor 0 >}},  and v{{< skew currentVersionAddMinor 1 >}} control planes.  Using the latest compatible version of kubectl helps avoid unforeseen issues.
- `['{{% heading "prerequisites" %}}', 'Install kubectl on Linux']`
  > Install kubectl on Linux  The following methods exist for installing kubectl on Linux:  - [Install kubectl binary with curl on Linux](#install-kubectl-binary-with-curl-on-linux)  - [Install using native package management](#install-using-native-package-management)  - [Install using other package management](#install-using-other-package-management)
- `['{{% heading "prerequisites" %}}', 'Install kubectl on Linux', 'Install kubectl binary with curl on Linux']`
  > Install kubectl binary with curl on Linux  1. Download the latest release with the command:  {{< tabs name="download_binary_linux" >}}  {{< tab name="x86-64" codelang="bash" >}}  curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"  {{< /tab >}}  {{< tab name="ARM64" codelang="bash" >}}  curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/arm64/kubectl"  {{< /tab >}}  {{< /tabs >}}  {{< note >}}  To download a specific version, replace the `$(curl -L -s https://dl.k8s.io/release/stable.txt)`…
- `['and then append (or prepend) ~/.local/bin to $PATH']`
  > and then append (or prepend) ~/.local/bin to $PATH  {{< /note >}}  1. Test to ensure the version you installed is up-to-date:  kubectl version --client  Or use this for detailed view of version:  kubectl version --client --output=yaml
- `['and then append (or prepend) ~/.local/bin to $PATH', 'Install using native package management']`
  > Install using native package management  {{< tabs name="kubectl_install" >}}  {{% tab name="Debian-based distributions" %}}  1. Update the `apt` package index and install packages needed to use the Kubernetes `apt` repository:  sudo apt-get update
- `['apt-transport-https may be a dummy package; if so, you can skip that package']`
  > apt-transport-https may be a dummy package; if so, you can skip that package  sudo apt-get install -y apt-transport-https ca-certificates curl gnupg  2. Download the public signing key for the Kubernetes package repositories. The same signing key is used for all repositories so you can disregard the version in the URL:

`verdict_relevant:` ______   `notes:` ______

---

## 92. `3d9a29ccbc00738c`  (git)

**Query** (zh / troubleshooting): 怎么通过 reflog 找回被删掉或丢失的提交？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-reflog.adoc`

**Claimed section** (unverified): `['git-reflog', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-reflog(1)  =============  NAME  ----  git-reflog - Manage reflog information  SYNOPSIS  --------  [synopsis]  git reflog [show] [<log-options>] [<ref>]  git reflog list  git reflog exists <ref>  git reflog write <ref> <old-oid> <new-oid> <message>  git reflog delete [--rewrite] [--updateref]  [--dry-run | -n] [--verbose] <ref>@{<specifier>}...  git reflog drop [--all [--single-worktree] | <refs>...]  git reflog expire [--expire=<time>] [--expire-unreachable=<time>]  [--rewrite] [--updateref] [--stale-fix]  [--dry-run | -n] [--verbose] [--all [--single-worktree] | <refs>...]  DESCRIPTION  -…

`verdict_relevant:` ______   `notes:` ______

---

## 93. `d49934e4614ba952`  (kubernetes)

**Query** (zh / config): 怎么用 ConfigMap 把配置注入到 Pod 里？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/configuration/configmap.md`

**Claimed section** (unverified): `['ConfigMaps']`

**Actual sections in the index:**

- `[]`
  > {{< glossary_definition term_id="configmap" prepend="A ConfigMap is" length="all" >}}  {{< caution >}}  ConfigMap does not provide secrecy or encryption.  If the data you want to store are confidential, use a  {{< glossary_tooltip text="Secret" term_id="secret" >}} rather than a ConfigMap,  or use additional (third party) tools to keep your data private.  {{< /caution >}}
- `['Motivation']`
  > Motivation  Use a ConfigMap for setting configuration data separately from application code.  For example, imagine that you are developing an application that you can run on your  own computer (for development) and in the cloud (to handle real traffic).  You write the code to look in an environment variable named `DATABASE_HOST`.  Locally, you set that variable to `localhost`. In the cloud, you set it to  refer to a Kubernetes {{< glossary_tooltip text="Service" term_id="service" >}}  that exposes the database component to your cluster.  This lets you fetch a container image running in the clo…
- `['Motivation', 'ConfigMap object']`
  > ConfigMap object  A ConfigMap is an {{< glossary_tooltip text="API object" term_id="object" >}}  that lets you store configuration for other objects to use. Unlike most  Kubernetes objects that have a `spec`, a ConfigMap has `data` and `binaryData`  fields. These fields accept key-value pairs as their values.  Both the `data`  field and the `binaryData` are optional. The `data` field is designed to  contain UTF-8 strings while the `binaryData` field is designed to  contain binary data as base64-encoded strings.  The name of a ConfigMap must be a valid  [DNS subdomain name](/docs/concepts/overv…
- `['Motivation', 'ConfigMaps and Pods']`
  > ConfigMaps and Pods  You can write a Pod `spec` that refers to a ConfigMap and configures the container(s)  in that Pod based on the data in the ConfigMap. The Pod and the ConfigMap must be in  the same {{< glossary_tooltip text="namespace" term_id="namespace" >}}.  {{< note >}}  The `spec` of a {{< glossary_tooltip text="static Pod" term_id="static-pod" >}} cannot refer to a ConfigMap  or any other API objects.  {{< /note >}}  Here's an example ConfigMap that has some keys with single values,  and other keys where the value looks like a fragment of a configuration  format.  apiVersion: v1  ki…
- `['property-like keys; each key maps to a simple value']`
  > property-like keys; each key maps to a simple value  player_initial_lives: "3"  ui_properties_file_name: "user-interface.properties"
- `['file-like keys']`
  > file-like keys  game.properties: |  enemy.types=aliens,monsters  player.maximum-lives=5  user-interface.properties: |  color.good=purple  color.bad=yellow  allow.textmode=true  There are four different ways that you can use a ConfigMap to configure  a container inside a Pod:  1. Inside a container command and args  1. Environment variables for a container  1. Add a file in read-only volume, for the application to read  1. Write code to run inside the Pod that uses the Kubernetes API to read a ConfigMap  These different methods lend themselves to different ways of modeling  the data being consu…

`verdict_relevant:` ______   `notes:` ______

---

## 94. `8d4866599ea25db6`  (go)

**Query** (en / concept): What does happens before mean in the Go memory model?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_mem.html`

**Claimed section** (unverified): `['The Go Memory Model', 'Memory Model']`

**Actual sections in the index:**

- `[]`
  > Introduction  The Go memory model specifies the conditions under which  reads of a variable in one goroutine can be guaranteed to  observe values produced by writes to the same variable in a different goroutine.  Advice  Programs that modify data being simultaneously accessed by multiple goroutines  must serialize such access.  To serialize access, protect the data with channel operations or other synchronization primitives  such as those in the sync  and sync/atomic packages.  If you must read the rest of this document to understand the behavior of your program,  you are being too clever.  Do…
- `[]`
  > meaning it has no program executions with read-write or write-write data races,  can only have outcomes explained by some sequentially consistent interleaving  of the goroutine executions.  (The proof is the same as Section 7 of Boehm and Adve's paper cited above.)  This property is called DRF-SC.  The intent of the formal definition is to match  the DRF-SC guarantee provided to race-free programs  by other languages, including C, C++, Java, JavaScript, Rust, and Swift.  Certain Go language operations such as goroutine creation and memory allocation  act as synchronization operations.  The eff…
- `[]`
  > the capacity of the channel corresponds to the maximum number of simultaneous uses,  sending an item acquires the semaphore, and receiving an item releases  the semaphore.  This is a common idiom for limiting concurrency.  This program starts a goroutine for every entry in the work list, but the  goroutines coordinate using the limit channel to ensure  that at most three are running work functions at a time.  var limit = make(chan int, 3)  func main() {  for _, w := range work {  go func(w func()) {  limit <- 1  w()  <-limit  }(w)  }  select{}  }  Locks  The sync package implements two lock da…
- `[]`
  > it must not allow a single read to observe multiple values,  and it must not allow a single write to write multiple values.  All the following examples assume that `*p` and `*q` refer to  memory locations accessible to multiple goroutines.  Not introducing data races into race-free programs means not moving  writes out of conditional statements in which they appear.  For example, a compiler must not invert the conditional in this program:  *p = 1  if cond {  *p = 2  }  That is, the compiler must not rewrite the program into this one:  *p = 2  if !cond {  *p = 1  }  If cond is false and another…

`verdict_relevant:` ______   `notes:` ______

---

## 95. `351e0b4ae5ede9dd`  (python)

**Query** (en / code_api): How do you serialize Python objects to JSON and parse JSON back?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/json.rst`

**Claimed section** (unverified): `['Basic Usage']`

**Actual sections in the index:**

- `[]`
  > :mod:`!json` --- JSON encoder and decoder  =========================================  .. module:: json  :synopsis: Encode and decode the JSON format.  **Source code:** :source:`Lib/json/__init__.py`  --------------  `JSON (JavaScript Object Notation) <https://json.org>`_, specified by  :rfc:`7159` (which obsoletes :rfc:`4627`) and by  `ECMA-404 <https://ecma-international.org/publications-and-standards/standards/ecma-404/>`_,  is a lightweight data interchange format inspired by  `JavaScript <https://en.wikipedia.org/wiki/JavaScript>`_ object literal syntax  (although it is not a strict subset…
- `[]`
  > Use ``(',', ': ')`` as default if *indent* is not ``None``.  .. versionchanged:: 3.6  All optional parameters are now :ref:`keyword-only <keyword-only_parameter>`.  .. function:: dumps(obj, *, skipkeys=False, ensure_ascii=True, \  check_circular=True, allow_nan=True, cls=None, \  indent=None, separators=None, default=None, \  sort_keys=False, **kw)  Serialize *obj* to a JSON formatted :class:`str` using this :ref:`conversion  table <py-to-json-table>`. The arguments have the same meaning as in  :func:`dump`.  .. note::  Keys in key/value pairs of JSON are always of the type :class:`str`. When…
- `[]`
  > *parse_int* is an optional function that will be called with the string of  every JSON int to be decoded. By default, this is equivalent to  ``int(num_str)``. This can be used to use another datatype or parser for  JSON integers (e.g. :class:`float`).  *parse_constant* is an optional function that will be called with one of the  following strings: ``'-Infinity'``, ``'Infinity'``, ``'NaN'``. This can be  used to raise an exception if invalid JSON numbers are encountered.  If *strict* is false (``True`` is the default), then control characters  will be allowed inside strings. Control characters…
- `['Let the base class default method raise the TypeError']`
  > Let the base class default method raise the TypeError  return super().default(o)  .. method:: encode(o)  Return a JSON string representation of a Python data structure, *o*. For  example::  >>> json.JSONEncoder().encode({"foo": ["bar", "baz"]})  '{"foo": ["bar", "baz"]}'  .. method:: iterencode(o)  Encode the given object, *o*, and yield each string representation as  available. For example::  for chunk in json.JSONEncoder().iterencode(bigobject):  mysocket.write(chunk)  Exceptions  ----------  .. exception:: JSONDecodeError(msg, doc, pos)  Subclass of :exc:`ValueError` with the following addi…
- `['Let the base class default method raise the TypeError']`
  > $ python -m json mp_films.json  [  {  "title": "And Now for Something Completely Different",  "year": 1971  },  {  "title": "Monty Python and the Holy Grail",  "year": 1975  }  ]  If *infile* is not specified, read from :data:`sys.stdin`.  .. option:: outfile  Write the output of the *infile* to the given *outfile*. Otherwise, write it  to :data:`sys.stdout`.  .. option:: --sort-keys  Sort the output of dictionaries alphabetically by key.  .. versionadded:: 3.5  .. option:: --no-ensure-ascii  Disable escaping of non-ascii characters, see :func:`json.dumps` for more information.  .. versionadde…

`verdict_relevant:` ______   `notes:` ______

---

## 96. `09cee24c64b1560c`  (go)

**Query** (en / code_api): How does panic propagate through the call stack and how can recover stop it?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Built-in functions', 'Handling panics']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 97. `a219f055ab24b4ff`  (python)

**Query** (en / troubleshooting): How do I capture and inspect the HTTP traffic between my client and a server?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/http.server.rst`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > :mod:`!http.server` --- HTTP servers  ====================================  .. module:: http.server  :synopsis: HTTP server and request handlers.  **Source code:** :source:`Lib/http/server.py`  .. index::  pair: WWW; server  pair: HTTP; protocol  single: URL  single: httpd  --------------  This module defines classes for implementing HTTP servers.  .. warning::  :mod:`!http.server` is not recommended for production. It only implements  :ref:`basic security checks <http.server-security>`.  .. include:: ../includes/wasm-notavail.rst  One class, :class:`HTTPServer`, is a :class:`socketserver.TCPS…
- `[]`
  > headers. Typically, this is not overridden, and it defaults to  :class:`http.client.HTTPMessage`.  .. attribute:: responses  This attribute contains a mapping of error code integers to two-element tuples  containing a short and long message. For example, ``{code: (shortmessage,  longmessage)}``. The *shortmessage* is usually used as the *message* key in an  error response, and *longmessage* as the *explain* key. It is used by  :meth:`send_response_only` and :meth:`send_error` methods.  A :class:`BaseHTTPRequestHandler` instance has the following methods:  .. method:: handle()  Calls :meth:`han…
- `[]`
  > Specifies the filenames that are treated as directory index pages.  Defaults to ``("index.html", "index.htm")``.  .. versionadded:: 3.12  .. attribute:: extensions_map  A dictionary mapping suffixes into MIME types, contains custom overrides  for the default system mappings. The mapping is used case-insensitively,  and so should contain only lower-cased keys.  .. versionchanged:: 3.9  This dictionary is no longer filled with the default system mappings,  but only contains overrides.  .. attribute:: extra_response_headers  A sequence of ``(name, value)`` pairs containing user-defined extra HTTP…
- `[]`
  > :meth:`BaseHTTPRequestHandler.send_response_only` assume sanitized input  and do not perform input validation such as checking for the presence of CRLF  sequences. Untrusted input may result in HTTP header injection attacks.  Earlier versions of Python did not scrub control characters from the  log messages emitted to stderr from ``python -m http.server`` or the  default :class:`BaseHTTPRequestHandler` ``.log_message``  implementation. This could allow remote clients connecting to your  server to send nefarious control codes to your terminal.  .. versionchanged:: 3.12  Control characters are s…

`verdict_relevant:` ______   `notes:` ______

---

## 98. `f0c5c95009933b4b`  (docker)

**Query** (zh / concept): Docker 镜像的分层结构是怎么回事，为什么说每层都是只读的？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/get-started/docker-concepts/building-images/understanding-image-layers.md`

**Claimed section** (unverified): `['Understanding the image layers']`

**Actual sections in the index:**

- `[]`
  > {{< youtube-embed wJwqtAkmtQA >}}
- `['Explanation']`
  > Explanation  As you learned in [What is an image?](../the-basics/what-is-an-image/), container images are composed of layers. And each of these layers, once created, are immutable. But, what does that actually mean? And how are those layers used to create the filesystem a container can use?
- `['Explanation', 'Image layers']`
  > Image layers  Each layer in an image contains a set of filesystem changes - additions, deletions, or modifications. Let’s look at a theoretical image:  1. The first layer adds basic commands and a package manager, such as apt.  2. The second layer installs a Python runtime and pip for dependency management.  3. The third layer copies in an application’s specific requirements.txt file.  4. The fourth layer installs that application’s specific dependencies.  5. The fifth layer copies in the actual source code of the application.  This example might look like:  ![screenshot of the flowchart showi…
- `['Explanation', 'Image layers', 'Stacking the layers']`
  > Stacking the layers  Layering is made possible by content-addressable storage and union filesystems. While this will get technical, here’s how it works:  1. After each layer is downloaded, it is extracted into its own directory on the host filesystem.  2. When you run a container from an image, a union filesystem is created where layers are stacked on top of each other, creating a new and unified view.  3. When the container starts, its root directory is set to the location of this unified directory, using `chroot`.  When the union filesystem is created, in addition to the image layers, a dire…
- `['Explanation', 'Try it out']`
  > Try it out  In this hands-on guide, you will create new image layers manually using the [`docker container commit`](https://docs.docker.com/reference/cli/docker/container/commit/) command. Note that you’ll rarely create images this way, as you’ll normally [use a Dockerfile](./writing-a-dockerfile.md). But, it makes it easier to understand how it’s all working.
- `['Explanation', 'Try it out', 'Create a base image']`
  > Create a base image  In this first step, you will create your own base image that you will then use for the following steps.  1. [Download and install](https://www.docker.com/products/docker-desktop/) Docker Desktop.  2. In a terminal, run the following command to start a new container:  $ docker run --name=base-container -ti ubuntu  Once the image has been downloaded and the container has started, you should see a new shell prompt. This is running inside your container. It will look similar to the following (the container ID will vary):  root@d8c5ca119fcd:/#  3. Inside the container, run the…

`verdict_relevant:` ______   `notes:` ______

---

## 99. `2fc6ee231aeb1288`  (go)

**Query** (en / troubleshooting): How do I install the Go toolchain with a package manager?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 100. `36c95e1f632943e3`  (go)

**Query** (zh / concept): Go 汇编里的 SB、FP、PC 这几个伪寄存器各代表什么含义？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", 'Symbols']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 101. `d80c3a7d67e146cc`  (postgresql)

**Query** (en / troubleshooting): What should I check when the PostgreSQL server fails to start?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/runtime.sgml`

**Claimed section** (unverified): `['Server Setup and Operation', 'Starting the Database Server']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/runtime.sgml -->  <chapter id="runtime">  <title>Server Setup and Operation</title>  <para>  This chapter discusses how to set up and run the database server,  and its interactions with the operating system.  </para>  <para>  The directions in this chapter assume that you are working with  plain <productname>PostgreSQL</productname> without any additional  infrastructure, for example a copy that you built from source  according to the directions in the preceding chapters.  If you are working with a pre-packaged or vendor-supplied  version of <productname>PostgreSQL</productna…
- `[]`
  > database and even become the database superuser. If you do not  trust other local users, we recommend you use one of  <command>initdb</command>'s <option>-W</option>, <option>--pwprompt</option>  or <option>--pwfile</option> options to assign a password to the  database superuser.<indexterm>  <primary>password</primary>  <secondary>of the superuser</secondary>  </indexterm>  Also, specify <option>-A scram-sha-256</option>  so that the default <literal>trust</literal> authentication  mode is not used; or modify the generated <filename>pg_hba.conf</filename>  file after running <command>initdb</…
- `[]`
  > background. For this, use the usual Unix shell syntax:  <screen>  $ <userinput>postgres -D /usr/local/pgsql/data &gt;logfile 2&gt;&amp;1 &amp;</userinput>  </screen>  It is important to store the server's <systemitem>stdout</systemitem> and  <systemitem>stderr</systemitem> output somewhere, as shown above. It will help  for auditing purposes and to diagnose problems. (See <xref  linkend="logfile-maintenance"/> for a more thorough discussion of log  file handling.)  </para>  <para>  The <command>postgres</command> program also takes a number of other  command-line options. For more information,…
- `[]`
  > increase the kernel limit.  </para>  <para>  Details about configuring <systemitem class="osname">System V</systemitem>  <acronym>IPC</acronym> facilities are given in <xref linkend="sysvipc"/>.  </para>  </sect2>  <sect2 id="client-connection-problems">  <title>Client Connection Problems</title>  <para>  Although the error conditions possible on the client side are quite  varied and application-dependent, a few of them might be directly  related to how the server was started. Conditions other than  those shown below should be documented with the respective client  application.  </para>  <para…
- `[]`
  > The runtime-computed parameter <xref linkend="guc-num-os-semaphores"/>  reports the number of semaphores required. This parameter can be viewed  before starting the server with a <command>postgres</command> command like:  <programlisting>  $ <userinput>postgres -D $PGDATA -C num_os_semaphores</userinput>  </programlisting>  </para>  <para>  Each set of 16 semaphores will  also contain a 17th semaphore which contains a <quote>magic  number</quote>, to detect collision with semaphore sets used by  other applications. The maximum number of semaphores in the system  is set by <varname>SEMMNS</varn…
- `[]`
  > <application>sysctl</application>. But it's still best to set up your preferred  values via <filename>/etc/sysctl.conf</filename>, so that the values will be  kept across reboots.  </para>  </listitem>  </varlistentry>  <varlistentry>  <term><systemitem class="osname">Solaris</systemitem></term>  <term><systemitem class="osname">illumos</systemitem></term>  <listitem>  <para>  The default shared memory and semaphore settings are usually good enough for most  <productname>PostgreSQL</productname> applications. Solaris defaults  to a <varname>SHMMAX</varname> of one-quarter of system <acronym>RA…

`verdict_relevant:` ______   `notes:` ______

---

## 102. `e439c8b304419768`  (kubernetes)

**Query** (zh / code_api): Taints 和 Tolerations 是怎么配合来决定 Pod 调度到哪些节点的？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/scheduling-eviction/taint-and-toleration.md`

**Claimed section** (unverified): `['Taints and Tolerations']`

**Actual sections in the index:**

- `[]`
  > [_Node affinity_](/docs/concepts/scheduling-eviction/assign-pod-node/#affinity-and-anti-affinity)  is a property of {{< glossary_tooltip text="Pods" term_id="pod" >}} that *attracts* them to  a set of {{< glossary_tooltip text="nodes" term_id="node" >}} (either as a preference or a  hard requirement). _Taints_ are the opposite -- they allow a node to repel a set of pods.  _Tolerations_ are applied to pods. Tolerations allow the scheduler to schedule pods with matching  taints. Tolerations allow scheduling but don't guarantee scheduling: the scheduler also  [evaluates other parameters](/docs/co…
- `['Concepts']`
  > Concepts  You add a taint to a node using [kubectl taint](/docs/reference/generated/kubectl/kubectl-commands#taint).  For example,  kubectl taint nodes node1 key1=value1:NoSchedule  places a taint on node `node1`. The taint has key `key1`, value `value1`, and taint effect `NoSchedule`.  This means that no pod will be able to schedule onto `node1` unless it has a matching toleration.  To remove the taint added by the command above, you can run:  kubectl taint nodes node1 key1=value1:NoSchedule-  You specify a toleration for a pod in the PodSpec. Both of the following tolerations "match" the  ta…
- `['Concepts', 'Numeric comparison operators {#numeric-comparison-operators}']`
  > Numeric comparison operators {#numeric-comparison-operators}  {{< feature-state feature_gate_name="TaintTolerationComparisonOperators" >}}  In addition to `Equal` and `Exists`, you can use numeric comparison operators  (`Gt` and `Lt`) to match taints with integer values. This is useful for threshold-based  scheduling, such as matching nodes by reliability level or SLA tier.  * `Gt` matches when the taint value is greater than the toleration value.  * `Lt` matches when the taint value is less than the toleration value.  For numeric operators, both the toleration and taint values must be valid i…
- `['Concepts', 'Example Use Cases']`
  > Example Use Cases  Taints and tolerations are a flexible way to steer pods *away* from nodes or evict  pods that shouldn't be running. A few of the use cases are  * **Dedicated Nodes**: If you want to dedicate a set of nodes for exclusive use by  a particular set of users, you can add a taint to those nodes (say,  `kubectl taint nodes nodename dedicated=groupName:NoSchedule`) and then add a corresponding  toleration to their pods (this would be done most easily by writing a custom  [admission controller](/docs/reference/access-authn-authz/admission-controllers/)).  The pods with the toleration…
- `['Concepts', 'Taint based Evictions']`
  > Taint based Evictions  {{< feature-state for_k8s_version="v1.18" state="stable" >}}  The node controller automatically taints a Node when certain conditions  are true. The following taints are built in:  * `node.kubernetes.io/not-ready`: Node is not ready. This corresponds to  the NodeCondition `Ready` being "`False`".  * `node.kubernetes.io/unreachable`: Node is unreachable from the node  controller. This corresponds to the NodeCondition `Ready` being "`Unknown`".  * `node.kubernetes.io/memory-pressure`: Node has memory pressure.  * `node.kubernetes.io/disk-pressure`: Node has disk pressure.…
- `['Concepts', 'Taint Nodes by Condition']`
  > Taint Nodes by Condition  The control plane, using the node {{<glossary_tooltip text="controller" term_id="controller">}},  automatically creates taints with a `NoSchedule` effect for  [node conditions](/docs/concepts/scheduling-eviction/node-pressure-eviction/#node-conditions).  The scheduler checks taints, not node conditions, when it makes scheduling  decisions. This ensures that node conditions don't directly affect scheduling.  For example, if the `DiskPressure` node condition is active, the control plane  adds the `node.kubernetes.io/disk-pressure` taint and does not schedule new pods  o…

`verdict_relevant:` ______   `notes:` ______

---

## 103. `b57a05d965b51dac`  (git)

**Query** (en / config): What core configuration options control file mode handling and line ending behavior?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/config/core.adoc`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > core.fileMode::  Tells Git if the executable bit of files in the working tree  is to be honored.  +  Some filesystems lose the executable bit when a file that is  marked as executable is checked out, or checks out a  non-executable file with executable bit on.  linkgit:git-clone[1] or linkgit:git-init[1] probe the filesystem  to see if it handles the executable bit correctly  and this variable is automatically set as necessary.  +  A repository, however, may be on a filesystem that handles  the filemode correctly, and this variable is set to 'true'  when created, but later may be made accessib…
- `[]`
  > the device number, if Git was compiled to use it), are  excluded from the check among these fields, leaving only the  whole-second part of mtime (and ctime, if `core.trustCtime`  is set) and the filesize to be checked.  +  There are implementations of Git that do not leave usable values in  some fields (e.g. JGit); by excluding these fields from the  comparison, the `minimal` mode may help interoperability when the  same repository is used by these other systems at the same time.  core.quotePath::  Commands that output paths (e.g. 'ls-files', 'diff'), will  quote "unusual" characters in the pa…
- `[]`
  > when the environment variable is set.  core.ignoreStat::  If true, Git will avoid using lstat() calls to detect if files have  changed by setting the "assume-unchanged" bit for those tracked files  which it has updated identically in both the index and working tree.  +  When files are modified outside of Git, the user will need to stage  the modified files explicitly (e.g. see 'Examples' section in  linkgit:git-update-index[1]).  Git will not normally detect changes to those files.  +  This is useful on systems where lstat() calls are very slow, such as  CIFS/Microsoft Windows.  +  False by de…
- `[]`
  > are not in a pack file. -1 is the zlib default. 0 means no  compression, and 1..9 are various speed/size tradeoffs, 9 being  slowest. If not set, defaults to core.compression. If that is  not set, defaults to 1 (best speed).  core.packedGitWindowSize::  Number of bytes of a pack file to map into memory in a  single mapping operation. Larger window sizes may allow  your system to process a smaller number of large pack files  more quickly. Smaller window sizes will negatively affect  performance due to increased calls to the operating system's  memory manager, but may improve performance when ac…
- `[]`
  > Note that these two variables are aliases of each other, and in modern  versions of Git you are free to use a string (e.g., `//` or `â��â��â��`) with  `commentChar`. Versions of Git prior to v2.45.0 will ignore  `commentString` but will reject a value of `commentChar` that consists  of more than a single ASCII byte. If you plan to use your config with  older and newer versions of Git, you may want to specify both:  +  [core]
- `['single character for older versions']`
  > single character for older versions  commentChar = "#"

`verdict_relevant:` ______   `notes:` ______

---

## 104. `5eba5ca4814cab8d`  (docker)

**Query** (zh / command): docker 命令的输出怎么用 --format 自定义成需要的字段？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/cli/formatting.md`

**Claimed section** (unverified): `['Format command and log output']`

**Actual sections in the index:**

- `[]`
  > Docker supports [Go templates](https://golang.org/pkg/text/template/) which you  can use to manipulate the output format of certain commands and log drivers.  Docker provides a set of basic functions to manipulate template elements.  All of these examples use the `docker inspect` command, but many other CLI  commands have a `--format` flag, and many of the CLI command references  include examples of customizing the output format.  > [!NOTE]  >  > When using the `--format` flag, you need to observe your shell environment.  > In a POSIX shell, you can run the following with a single quote:  >  >…
- `['join']`
  > join  `join` concatenates a list of strings to create a single string.  It puts a separator between each element in the list.  $ docker inspect --format '{{join .Args " , "}}' container
- `['join', 'table']`
  > table  `table` specifies which fields you want to see its output.  $ docker image list --format "table {{.ID}}\t{{.Repository}}\t{{.Tag}}\t{{.Size}}"
- `['join', 'json']`
  > json  `json` encodes an element as a json string.  $ docker inspect --format '{{json .Mounts}}' container
- `['join', 'lower']`
  > lower  `lower` transforms a string into its lowercase representation.  $ docker inspect --format "{{lower .Name}}" container
- `['join', 'split']`
  > split  `split` slices a string into a list of strings separated by a separator.  $ docker inspect --format '{{split .Image ":"}}' container

`verdict_relevant:` ______   `notes:` ______

---

## 105. `929eb982cfd4f8d6`  (git)

**Query** (en / troubleshooting): How do I configure Git to sign commits with GPG and verify those signatures?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-notes.adoc`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > git-notes(1)  ============  NAME  ----  git-notes - Add or inspect object notes  SYNOPSIS  --------  [synopsis]  git notes [list [<object>]]  git notes add [-f] [--allow-empty] [--[no-]separator | --separator=<paragraph-break>] [--[no-]stripspace] [-F <file> | -m <msg> | (-c | -C) <object>] [-e] [<object>]  git notes copy [-f] ( --stdin | <from-object> [<to-object>] )  git notes append [--allow-empty] [--[no-]separator | --separator=<paragraph-break>] [--[no-]stripspace] [-F <file> | -m <msg> | (-c | -C) <object>] [-e] [<object>]  git notes edit [--allow-empty] [<object>] [--[no-]stripspace]…
- `[]`
  > Allow an empty note object to be stored. The default behavior is  to automatically remove empty notes.  `--separator=<paragraph-break>`::  `--separator`::  `--no-separator`::  Specify a string used as a custom inter-paragraph separator  (a newline is added at the end as needed). If `--no-separator`, no  separators will be added between paragraphs. Defaults to a blank  line.  `--stripspace`::  `--no-stripspace`::  Clean up whitespace. Specifically (see  linkgit:git-stripspace[1]):  +  --  - remove trailing whitespace from all lines  - collapse multiple consecutive empty lines into one empty lin…
- `[]`
  > Colon-delimited list of refs or globs indicating which refs,  in addition to the default from `core.notesRef` or  `GIT_NOTES_REF`, to read notes from when showing commit  messages.  This overrides the `notes.displayRef` setting.  +  A warning will be issued for refs that do not exist, but a glob that  does not match any refs is silently ignored.  `GIT_NOTES_REWRITE_MODE`::  When copying notes during a rewrite, what to do if the target  commit already has a note.  Must be one of `overwrite`, `concatenate`, `cat_sort_uniq`, or `ignore`.  This overrides the `core.rewriteMode` setting.  `GIT_NOTES…

`verdict_relevant:` ______   `notes:` ______

---

## 106. `ffc3392488c0801d`  (kubernetes)

**Query** (zh / command): 怎么用 kubectl 把节点安全地排空（drain）以进行维护？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/administer-cluster/safely-drain-node.md`

**Claimed section** (unverified): `['Safely Drain a Node']`

**Actual sections in the index:**

- `[]`
  > This page shows how to safely drain a {{< glossary_tooltip text="node" term_id="node" >}},  optionally respecting the PodDisruptionBudget you have defined.
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  This task assumes that you have met the following prerequisites:  1. You do not require your applications to be highly available during the  node drain, or  1. You have read about the [PodDisruptionBudget](/docs/concepts/workloads/pods/disruptions/) concept,  and have [configured PodDisruptionBudgets](/docs/tasks/run-application/configure-pdb/) for  applications that need them.
- `['{{% heading "prerequisites" %}}', '(Optional) Configure a disruption budget {#configure-poddisruptionbudget}']`
  > (Optional) Configure a disruption budget {#configure-poddisruptionbudget}  To ensure that your workloads remain available during maintenance, you can  configure a [PodDisruptionBudget](/docs/concepts/workloads/pods/disruptions/).  If availability is important for any applications that run or could run on the node(s)  that you are draining, [configure a PodDisruptionBudgets](/docs/tasks/run-application/configure-pdb/)  first and then continue following this guide.  It is recommended to set `AlwaysAllow` [Unhealthy Pod Eviction Policy](/docs/tasks/run-application/configure-pdb/#unhealthy-pod-evi…
- `['{{% heading "prerequisites" %}}', 'Use `kubectl drain` to remove a node from service']`
  > Use `kubectl drain` to remove a node from service  You can use `kubectl drain` to safely evict all of your pods from a  node before you perform maintenance on the node (e.g. kernel upgrade,  hardware maintenance, etc.). Safe evictions allow the pod's containers  to [gracefully terminate](/docs/concepts/workloads/pods/pod-lifecycle/#pod-termination)  and will respect the PodDisruptionBudgets you have specified.  {{< note >}}  By default `kubectl drain` ignores certain system pods on the node  that cannot be killed; see  the [kubectl drain](/docs/reference/generated/kubectl/kubectl-commands/#dra…
- `['{{% heading "prerequisites" %}}', 'Draining multiple nodes in parallel']`
  > Draining multiple nodes in parallel  The `kubectl drain` command should only be issued to a single node at a  time. However, you can run multiple `kubectl drain` commands for  different nodes in parallel, in different terminals or in the  background. Multiple drain commands running concurrently will still  respect the PodDisruptionBudget you specify.  For example, if you have a StatefulSet with three replicas and have  set a PodDisruptionBudget for that set specifying `minAvailable: 2`,  `kubectl drain` only evicts a pod from the StatefulSet if all three  replicas pods are [healthy](/docs/task…
- `['{{% heading "prerequisites" %}}', 'The Eviction API {#eviction-api}']`
  > The Eviction API {#eviction-api}  If you prefer not to use [kubectl drain](/docs/reference/generated/kubectl/kubectl-commands/#drain) (such as  to avoid calling to an external command, or to get finer control over the pod  eviction process), you can also programmatically cause evictions using the  eviction API.  For more information, see [API-initiated eviction](/docs/concepts/scheduling-eviction/api-eviction/).

`verdict_relevant:` ______   `notes:` ______

---

## 107. `67b147966ad9c14a`  (git)

**Query** (zh / concept): Git 里怎么用 SHA-1、分支名、tag 或相对记号来指定一个提交？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/revisions.adoc`

**Claimed section** (unverified): `['SPECIFYING REVISIONS']`

**Actual sections in the index:**

- `[]`
  > SPECIFYING REVISIONS  --------------------  A revision parameter '<rev>' typically, but not necessarily, names a  commit object. It uses what is called an 'extended SHA-1'  syntax. Here are various ways to spell object names. The  ones listed near the end of this list name trees and  blobs contained in a commit.  NOTE: This document shows the "raw" syntax as seen by git. The shell  and other UIs might require additional quoting to protect special  characters and to avoid word splitting.  '<sha1>', e.g. 'dae86e1950b1277e545cee180551750029cfe735', 'dae86e'::  The full SHA-1 object name (40-byte…
- `[]`
  > A suffix '{tilde}' to a revision parameter means the first parent of  that commit object.  A suffix '{tilde}<n>' to a revision parameter means the commit  object that is the <n>th generation ancestor of the named  commit object, following only the first parents. I.e. '<rev>{tilde}3' is  equivalent to '<rev>{caret}{caret}{caret}' which is equivalent to  '<rev>{caret}1{caret}1{caret}1'. See below for an illustration of  the usage of this form.  '<rev>{caret}{<type>}', e.g. 'v0.99.8{caret}\{commit\}'::  A suffix '{caret}' followed by an object type name enclosed in  brace pair means dereference t…
- `[]`
  > empty range that is both reachable and unreachable from HEAD.  Commands that are specifically designed to take two distinct ranges  (e.g. "git range-diff R1 R2" to compare two ranges) do exist, but  they are exceptions. Unless otherwise noted, all "git" commands  that operate on a set of commits work on a single revision range.  In other words, writing two "two-dot range notation" next to each  other, e.g.  $ git log A..B C..D  does *not* specify two revision ranges for most commands. Instead  it will name a single connected set of commits, i.e. those that are  reachable from either B or D but…

`verdict_relevant:` ______   `notes:` ______

---

## 108. `41881cfdffe23dd7`  (python)

**Query** (zh / troubleshooting): 为什么 Python 会报 UnboundLocalError，明明变量在函数外面定义过？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/faq/programming.rst`

**Claimed section** (unverified): `['Programming FAQ', 'Core language']`

**Actual sections in the index:**

- `[]`
  > :tocdepth: 2  ===============  Programming FAQ  ===============  .. only:: html  .. contents::  General questions  =================  Is there a source code-level debugger with breakpoints and single-stepping?  ---------------------------------------------------------------------------  Yes.  Several debuggers for Python are described below, and the built-in function  :func:`breakpoint` allows you to drop into any of them.  The pdb module is a simple but adequate console-mode debugger for Python. It is  part of the standard Python library, and is :mod:`documented in the Library  Reference Manu…
- `[]`
  > Assume you use a for loop to define a few different lambdas (or even plain  functions), for example::  >>> squares = []  >>> for x in range(5):  ... squares.append(lambda: x**2)  This gives you a list that contains 5 lambdas that calculate ``x**2``. You  might expect that, when called, they would return, respectively, ``0``, ``1``,  ``4``, ``9``, and ``16``. However, when you actually try you will see that  they all return ``16``::  >>> squares[2]()  16  >>> squares[4]()  16  This happens because ``x`` is not local to the lambdas, but is defined in  the outer scope, and it is accessed when the…
- `[]`
  > and class instances can lead to confusion.  Because of this feature, it is good programming practice to not use mutable  objects as default values. Instead, use ``None`` as the default value and  inside the function, check if the parameter is ``None`` and create a new  list/dictionary/whatever if it is. For example, don't write::  def foo(mydict={}):  ...  but::  def foo(mydict=None):  if mydict is None:  mydict = {} # create a new dict for local namespace  This feature can be useful. When you have a function that's time-consuming to  compute, a common technique is to cache the parameters and…
- `['Callers can only provide two parameters and optionally pass _cache by keyword']`
  > Callers can only provide two parameters and optionally pass _cache by keyword  def expensive(arg1, arg2, *, _cache={}):  if (arg1, arg2) in _cache:  return _cache[(arg1, arg2)]
- `['Calculate the value']`
  > Calculate the value  result = ... expensive computation ...  _cache[(arg1, arg2)] = result # Store result in the cache  return result  You could use a global variable containing a dictionary instead of the default  value; it's a matter of taste.  How can I pass optional or keyword parameters from one function to another?  ---------------------------------------------------------------------------  Collect the arguments using the ``*`` and ``**`` specifiers in the function's  parameter list; this gives you the positional arguments as a tuple and the  keyword arguments as a dictionary. You can t…
- `['Calculate the value']`
  > ...  >>> def func4(args):  ... args.a = 'new-value' # args is a mutable Namespace  ... args.b = args.b + 1 # change object in-place  ...  >>> args = Namespace(a='old-value', b=99)  >>> func4(args)  >>> vars(args)  {'a': 'new-value', 'b': 100}  There's almost never a good reason to get this complicated.  Your best choice is to return a tuple containing the multiple results.  How do you make a higher order function in Python?  --------------------------------------------------  You have two choices: you can use nested scopes or you can use callable objects.  For example, suppose you wanted to de…

`verdict_relevant:` ______   `notes:` ______

---

## 109. `de0b21c3678787e1`  (python)

**Query** (zh / concept): Python 里的对象、值和类型三者是什么关系？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/reference/datamodel.rst`

**Claimed section** (unverified): `['Data model', 'Objects, values and types']`

**Actual sections in the index:**

- `[]`
  > .. _datamodel:  **********  Data model  **********  .. _objects:  Objects, values and types  =========================  .. index::  single: object  single: data  :dfn:`Objects` are Python's abstraction for data. All data in a Python program  is represented by objects or by relations between objects. Even code is  represented by objects.  .. index::  pair: built-in function; id  pair: built-in function; type  single: identity of an object  single: value of an object  single: type of an object  single: mutable object  single: immutable object  Every object has an identity, a type and a value. An…
- `[]`
  > This type has a single value. There is a single object with this value. This  object is accessed through the built-in name :data:`NotImplemented`. Numeric methods  and rich comparison methods should return this value if they do not implement the  operation for the operands provided. (The interpreter will then try the  reflected operation, or some other fallback, depending on the operator.) It  should not be evaluated in a boolean context.  See  :ref:`implementing-the-arithmetic-operations`  for more details.  .. versionchanged:: 3.9  Evaluating :data:`NotImplemented` in a boolean context was d…
- `[]`
  > pair: string; item  single: Unicode  A string (:class:`str`) is a sequence of values that represent  :dfn:`characters`, or more formally, *Unicode code points*.  All the code points in the range ``0`` to ``0x10FFFF`` can be  represented in a string.  Python doesn't have a dedicated *character* type.  Instead, every code point in the string is represented as a string  object with length ``1``.  The built-in function :func:`ord`  converts a code point from its string form to an integer in the  range ``0`` to ``0x10FFFF``; :func:`chr` converts an integer in the range  ``0`` to ``0x10FFFF`` to the…
- `[]`
  > These are the types to which the function call operation (see section  :ref:`calls`) can be applied:  .. _user-defined-funcs:  User-defined functions  ^^^^^^^^^^^^^^^^^^^^^^  .. index::  pair: user-defined; function  pair: object; function  pair: object; user-defined function  A user-defined function object is created by a function definition (see  section :ref:`function`). It should be called with an argument list  containing the same number of items as the function's formal parameter  list.  Special read-only attributes  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~  .. index::  single: __builtins__ (functio…
- `[]`
  > single: generator; iterator  A function or method which uses the :keyword:`yield` statement (see section  :ref:`yield`) is called a :dfn:`generator function`. Such a function, when  called, always returns an :term:`iterator` object which can be used to  execute the body of the function: calling the iterator's  :meth:`iterator.__next__` method will cause the function to execute until  it provides a value using the :keyword:`!yield` statement. When the  function executes a :keyword:`return` statement or falls off the end, a  :exc:`StopIteration` exception is raised and the iterator will have  re…
- `[]`
  > If the module is top-level (that is, not a part of any specific package)  then the attribute should be set to ``''`` (the empty string). Otherwise,  it should be set to the name of the module's package (which can be equal to  :attr:`module.__name__` if the module itself is a package). See :pep:`366`  for further details.  This attribute is used instead of :attr:`~module.__name__` to calculate  explicit relative imports for main modules. It defaults to ``None`` for  modules created dynamically using the :class:`types.ModuleType` constructor;  use :func:`importlib.util.module_from_spec` instead…

`verdict_relevant:` ______   `notes:` ______

---

## 110. `ef22011e8fbd3c67`  (go)

**Query** (zh / code_api): Go 的 reflect 包怎么用，能动态修改变量的值吗？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 111. `58cc4f445f71a903`  (docker)

**Query** (en / config): How do you set environment variables inside a container when running it?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/how-tos/environment-variables/set-environment-variables.md`

**Claimed section** (unverified): `["Set environment variables within your container's environment"]`

**Actual sections in the index:**

- `[]`
  > A container's environment is not set until there's an explicit entry in the service configuration to make this happen. With Compose, there are two ways you can set environment variables in your containers with your Compose file.  >[!TIP]  >  > Don't use environment variables to pass sensitive information, such as passwords, in to your containers. Use [secrets](../use-secrets.md) instead.
- `['Use the `environment` attribute']`
  > Use the `environment` attribute  You can set environment variables directly in your container's environment with the  [`environment` attribute](/reference/compose-file/services.md#environment) in your `compose.yaml`.  It supports both list and mapping syntax:  services:  webapp:  environment:  DEBUG: "true"  is equivalent to  services:  webapp:  environment:  - DEBUG=true  See [`environment` attribute](/reference/compose-file/services.md#environment) for more examples on how to use it.
- `['Use the `environment` attribute', 'Additional information']`
  > Additional information  - You can choose not to set a value and pass the environment variables from your shell straight through to your containers. It works in the same way as `docker run -e VARIABLE ...`:  web:  environment:  - DEBUG  The value of the `DEBUG` variable in the container is taken from the value for the same variable in the shell in which Compose is run. Note that in this case no warning is issued if the `DEBUG` variable in the shell environment is not set.  - You can also take advantage of [interpolation](variable-interpolation.md#interpolation-syntax). In the following example,…
- `['Use the `environment` attribute', 'Use the `env_file` attribute']`
  > Use the `env_file` attribute  A container's environment can also be set using [`.env` files](variable-interpolation.md#env-file) along with the [`env_file` attribute](/reference/compose-file/services.md#env_file).  services:  webapp:  env_file: "webapp.env"  Using an `.env` file lets you use the same file for use by a plain `docker run --env-file ...` command, or to share the same `.env` file within multiple services without the need to duplicate a long `environment` YAML block.  It can also help you keep your environment variables separate from your main configuration file, providing a more o…
- `['Use the `environment` attribute', 'Use the `env_file` attribute', 'Additional information']`
  > Additional information  - If multiple files are specified, they are evaluated in order and can override values set in previous files.  - As of Docker Compose version 2.24.0, you can set your `.env` file, defined by the `env_file` attribute, to be optional by using the `required` field. When `required` is set to `false` and the `.env` file is missing, Compose silently ignores the entry.  env_file:  - path: ./default.env  required: true # default  - path: ./override.env  required: false  - As of Docker Compose version 2.30.0, you can use an alternative file format for the `env_file` with the `fo…
- `['Use the `environment` attribute', 'Set environment variables with `docker compose run --env`']`
  > Set environment variables with `docker compose run --env`  Similar to `docker run --env`, you can set environment variables temporarily with `docker compose run --env` or its short form `docker compose run -e`:  $ docker compose run -e DEBUG=1 web python console.py

`verdict_relevant:` ______   `notes:` ______

---

## 112. `e5289494580a39f2`  (go)

**Query** (en / concept): What is an interface type in Go, and what does it mean for a type to satisfy an interface?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Types', 'Interface types']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 113. `d455a588870ef33e`  (postgresql)

**Query** (en / concept): How do you define tables, their columns, and default values in PostgreSQL?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/ddl.sgml`

**Claimed section** (unverified): `['Data Definition', 'Table Basics']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/ddl.sgml -->  <chapter id="ddl">  <title>Data Definition</title>  <para>  This chapter covers how one creates the database structures that  will hold one's data. In a relational database, the raw data is  stored in tables, so the majority of this chapter is devoted to  explaining how tables are created and modified and what features are  available to control what data is stored in the tables.  Subsequently, we discuss how tables can be organized into  schemas, and how privileges can be assigned to tables. Finally,  we will briefly look at other features that affect the data s…
- `[]`
  > If no default value is declared explicitly, the default value is the  null value. This usually makes sense because a null value can  be considered to represent unknown data.  </para>  <para>  In a table definition, default values are listed after the column  data type. For example:  <programlisting>  CREATE TABLE products (  product_no integer,  name text,  price numeric <emphasis>DEFAULT 9.99</emphasis>  );  </programlisting>  </para>  <para>  The default value can be an expression, which will be  evaluated whenever the default value is inserted  (<emphasis>not</emphasis> when the table is cr…
- `[]`
  > whenever the row changes and cannot be overridden. A column default may  not refer to other columns of the table; a generation expression would  normally do so. A column default can use volatile functions, for example  <literal>random()</literal> or functions referring to the current time;  this is not allowed for generated columns.  </para>  <para>  Several restrictions apply to the definition of generated columns and  tables involving generated columns:  <itemizedlist>  <listitem>  <para>  The generation expression can only use immutable functions and cannot  use subqueries or reference anyt…
- `[]`
  > A check constraint is the most generic constraint type. It allows  you to specify that the value in a certain column must satisfy a  Boolean (truth-value) expression. For instance, to require positive  product prices, you could use:  <programlisting>  CREATE TABLE products (  product_no integer,  name text,  price numeric <emphasis>CHECK (price &gt; 0)</emphasis>  );  </programlisting>  </para>  <para>  As you see, the constraint definition comes after the data type,  just like default value definitions. Default values and  constraints can be listed in any order. A check constraint  consists o…
- `[]`
  > name text <emphasis>CONSTRAINT products_name_not_null</emphasis> NOT NULL,  price numeric  );  </programlisting>  </para>  <para>  A not-null constraint is usually written as a column constraint. The  syntax for writing it as a table constraint is  <programlisting>  CREATE TABLE products (  product_no integer,  name text,  price numeric,  <emphasis>NOT NULL product_no</emphasis>,  <emphasis>NOT NULL name</emphasis>  );  </programlisting>  But this syntax is not standard and mainly intended for use by  <application>pg_dump</application>.  </para>  <para>  A not-null constraint is functionally e…
- `[]`
  > makes use of a primary key if one has been declared; for example,  the primary key defines the default target column(s) for foreign keys  referencing its table.  </para>  </sect2>  <sect2 id="ddl-constraints-fk">  <title>Foreign Keys</title>  <indexterm>  <primary>foreign key</primary>  </indexterm>  <indexterm>  <primary>constraint</primary>  <secondary>foreign key</secondary>  </indexterm>  <indexterm>  <primary>referential integrity</primary>  </indexterm>  <para>  A foreign key constraint specifies that the values in a column (or  a group of columns) must match the values appearing in some…

`verdict_relevant:` ______   `notes:` ______

---

## 114. `65c2a4a5901af62d`  (postgresql)

**Query** (en / troubleshooting): A query is stuck waiting for a lock; how do you see which locks are held?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/monitoring.sgml`

**Claimed section** (unverified): `['Monitoring Database Activity', 'Viewing Locks']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/monitoring.sgml -->  <chapter id="monitoring">  <title>Monitoring Database Activity</title>  <indexterm zone="monitoring">  <primary>monitoring</primary>  <secondary>database activity</secondary>  </indexterm>  <indexterm zone="monitoring">  <primary>database activity</primary>  <secondary>monitoring</secondary>  </indexterm>  <para>  A database administrator frequently wonders, <quote>What is the system  doing right now?</quote>  This chapter discusses how to find that out.  </para>  <para>  Several tools are available for monitoring database activity and  analyzing performa…
- `[]`
  > </para>  <para>  Cumulative statistics are collected in shared memory. Every  <productname>PostgreSQL</productname> process collects statistics locally,  then updates the shared data at appropriate intervals. When a server,  including a physical replica, shuts down cleanly, a permanent copy of the  statistics data is stored in the <filename>pg_stat</filename> subdirectory,  so that statistics can be retained across server restarts. In contrast,  when starting from an unclean shutdown (e.g., after an immediate shutdown,  a server crash, starting from a base backup, and point-in-time recovery),…
- `[]`
  > <entry>View Name</entry>  <entry>Description</entry>  </row>  </thead>  <tbody>  <!-- everything related to global objects, alphabetically -->  <row>  <entry><structname>pg_stat_archiver</structname><indexterm><primary>pg_stat_archiver</primary></indexterm></entry>  <entry>One row only, showing statistics about the  WAL archiver process's activity. See  <link linkend="monitoring-pg-stat-archiver-view">  <structname>pg_stat_archiver</structname></link> for details.  </entry>  </row>  <row>  <entry><structname>pg_stat_bgwriter</structname><indexterm><primary>pg_stat_bgwriter</primary></indexterm…
- `[]`
  > process is a parallel group leader or leader apply worker, or does not  participate in any parallel operation.  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>usesysid</structfield> <type>oid</type>  </para>  <para>  OID of the user logged into this backend  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>usename</structfield> <type>name</type>  </para>  <para>  Name of the user logged into this backend  </para></entry>  </row>  <row>  <entry role="catalog_table_entr…
- `[]`
  > a backend process to perform operations in parallel.  </para>  </listitem>  <listitem>  <para>  <literal>REPACK decoding worker</literal>: A background process that  decodes WAL for <command>REPACK (CONCURRENTLY)</command>.  </para>  </listitem>  <listitem>  <para>  <literal>slotsync worker</literal>: The background process that  synchronizes logical replication slots on a streaming replication  standby server, active when <xref linkend="guc-sync-replication-slots"/>  is set to <literal>on</literal>.  </para>  </listitem>  <listitem>  <para>  <literal>standalone backend</literal>: The backend…
- `[]`
  > reverse DNS lookup of <structfield>client_addr</structfield>. This field will  only be non-null for IP connections, and only when <xref linkend="guc-log-hostname"/> is enabled.  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>client_port</structfield> <type>integer</type>  </para>  <para>  TCP port number that the client is using for communication  with this WAL sender, or <literal>-1</literal> if a Unix socket is used  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>…

`verdict_relevant:` ______   `notes:` ______

---

## 115. `9ac9ed71db42e66a`  (docker)

**Query** (en / troubleshooting): The Docker daemon won't start or behaves erratically; what should I check?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/daemon/troubleshoot.md`

**Claimed section** (unverified): `['Troubleshooting the Docker daemon']`

**Actual sections in the index:**

- `[]`
  > This page describes how to troubleshoot and debug the daemon if you run into  issues.  You can turn on debugging on the daemon to learn about the runtime activity of  the daemon and to aid in troubleshooting. If the daemon is unresponsive, you can  also [force a full stack trace](logs.md#force-a-stack-trace-to-be-logged) of all  threads to be added to the daemon log by sending the `SIGUSR` signal to the  Docker daemon.
- `['Daemon']`
  > Daemon
- `['Daemon', 'Unable to connect to the Docker daemon']`
  > Unable to connect to the Docker daemon  Cannot connect to the Docker daemon. Is 'docker daemon' running on this host?  This error may indicate:  - The Docker daemon isn't running on your system. Start the daemon and try  running the command again.  - Your Docker client is attempting to connect to a Docker daemon on a different  host, and that host is unreachable.
- `['Daemon', 'Unable to connect to the Docker daemon', 'Check whether Docker is running']`
  > Check whether Docker is running  The operating-system independent way to check whether Docker is running is to  ask Docker, using the `docker info` command.  You can also use operating system utilities, such as  `sudo systemctl is-active docker` or `sudo status docker` or  `sudo service docker status`, or checking the service status using Windows  utilities.  Finally, you can check in the process list for the `dockerd` process, using  commands like `ps` or `top`.
- `['Daemon', 'Unable to connect to the Docker daemon', 'Check whether Docker is running', 'Check which host your client is connecting to']`
  > Check which host your client is connecting to  To see which host your client is connecting to, check the value of the  `DOCKER_HOST` variable in your environment.  $ env | grep DOCKER_HOST  If this command returns a value, the Docker client is set to connect to a Docker  daemon running on that host. If it's unset, the Docker client is set to connect  to the Docker daemon running on the local host. If it's set in error, use the  following command to unset it:  $ unset DOCKER_HOST  You may need to edit your environment in files such as `~/.bashrc` or  `~/.profile` to prevent the `DOCKER_HOST` va…
- `['Daemon', 'Unable to connect to the Docker daemon', 'Troubleshoot conflicts between the `daemon.json` and startup scripts']`
  > Troubleshoot conflicts between the `daemon.json` and startup scripts  If you use a `daemon.json` file and also pass options to the `dockerd` command  manually or using start-up scripts, and these options conflict, Docker fails to  start with an error such as:  unable to configure the Docker daemon with file /etc/docker/daemon.json:  the following directives are specified both as a flag and in the configuration  file: hosts: (from flag: [unix:///var/run/docker.sock], from file: [tcp://127.0.0.1:2376])  If you see an error similar to this one and you are starting the daemon manually  with flags,…

`verdict_relevant:` ______   `notes:` ______

---

## 116. `e87215f62ab9904e`  (git)

**Query** (zh / command): 怎么在 Git 和 SVN 仓库之间来回同步代码？

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-daemon.adoc`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > git-daemon(1)  =============  NAME  ----  git-daemon - A really simple server for Git repositories  SYNOPSIS  --------  [synopsis]  git daemon [--verbose] [--syslog] [--export-all]  [--timeout=<n>] [--init-timeout=<n>] [--max-connections=<n>]  [--strict-paths] [--base-path=<path>] [--base-path-relaxed]  [--user-path | --user-path=<path>]  [--interpolated-path=<pathtemplate>]  [--reuseaddr] [--detach] [--pid-file=<file>]  [--enable=<service>] [--disable=<service>]  [--allow-override=<service>] [--forbid-override=<service>]  [--access-hook=<path>] [--[no-]informative-errors]  [--inetd |  [--list…
- `[]`
  > the existence of unexported repositories. When informative  errors are not enabled, all errors report "access denied" to the  client. The default is `--no-informative-errors`.  `--access-hook=<path>`::  Every time a client connects, first run an external command  specified by the <path> with service name (e.g. "upload-pack"),  path to the repository, hostname (`%H`), canonical hostname  (`%CH`), IP address (`%IP`), and TCP port (`%P`) as its command-line  arguments. The external command can decide to decline the  service by exiting with a non-zero status (or to allow it by  exiting with a zero…

`verdict_relevant:` ______   `notes:` ______

---

## 117. `eb914c3b58aec654`  (docker)

**Query** (zh / concept): Swarm 模式里的服务（service）和任务（task）是什么关系？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/swarm/how-swarm-mode-works/services.md`

**Claimed section** (unverified): `['How services work']`

**Actual sections in the index:**

- `[]`
  > To deploy an application image when Docker Engine is in Swarm mode, you create a  service. Frequently a service is the image for a microservice within the  context of some larger application. Examples of services might include an HTTP  server, a database, or any other type of executable program that you wish to run  in a distributed environment.  When you create a service, you specify which container image to use and which  commands to execute inside running containers. You also define options for the  service including:  - The port where the swarm makes the service available outside the swarm…
- `['Services, tasks, and containers']`
  > Services, tasks, and containers  When you deploy the service to the swarm, the swarm manager accepts your service  definition as the desired state for the service. Then it schedules the service  on nodes in the swarm as one or more replica tasks. The tasks run independently  of each other on nodes in the swarm.  For example, imagine you want to load balance between three instances of an HTTP  listener. The diagram below shows an HTTP listener service with three replicas.  Each of the three instances of the listener is a task in the swarm.  ![ HTTP listener service with three replicas](../image…
- `['Services, tasks, and containers', 'Tasks and scheduling']`
  > Tasks and scheduling  A task is the atomic unit of scheduling within a swarm. When you declare a  desired service state by creating or updating a service, the orchestrator  realizes the desired state by scheduling tasks. For instance, you define a  service that instructs the orchestrator to keep three instances of an HTTP  listener running at all times. The orchestrator responds by creating three  tasks. Each task is a slot that the scheduler fills by spawning a container. The  container is the instantiation of the task. If an HTTP listener task subsequently  fails its health check or crashes,…
- `['Services, tasks, and containers', 'Tasks and scheduling', 'Pending services']`
  > Pending services  A service may be configured in such a way that no node currently in the  swarm can run its tasks. In this case, the service remains in state `pending`.  Here are a few examples of when a service might remain in state `pending`.  > [!TIP]  > If your only intention is to prevent a service from  > being deployed, scale the service to 0 instead of trying to configure it in  > such a way that it remains in `pending`.  - If all nodes are paused or drained, and you create a service, it is  pending until a node becomes available. In reality, the first node to become  available gets a…
- `['Services, tasks, and containers', 'Replicated and global services']`
  > Replicated and global services  There are two types of service deployments, replicated and global.  For a replicated service, you specify the number of identical tasks you want to  run. For example, you decide to deploy an HTTP service with three replicas, each  serving the same content.  A global service is a service that runs one task on every node. There is no  pre-specified number of tasks. Each time you add a node to the swarm, the  orchestrator creates a task and the scheduler assigns the task to the new node.  Good candidates for global services are monitoring agents, anti-virus scanner…
- `['Services, tasks, and containers', 'Learn more']`
  > Learn more  - Read about how Swarm mode [nodes](nodes.md) work.  - Learn how [PKI](pki.md) works in Swarm mode.

`verdict_relevant:` ______   `notes:` ______

---

## 118. `24d868cb456e7f85`  (git)

**Query** (en / config): Where does Git store its configuration, and what do user.name and user.email control?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-config.adoc`

**Claimed section** (unverified): `['git-config', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > git-config(1)  =============  NAME  ----  git-config - Get and set repository or global options  SYNOPSIS  --------  [verse]  'git config list' [<file-option>] [<display-option>] [--includes]  'git config get' [<file-option>] [<display-option>] [--includes] [--all] [--regexp] [--value=<pattern>] [--fixed-value] [--default=<default>] [--url=<url>] <name>  'git config set' [<file-option>] [--type=<type>] [--all] [--value=<pattern>] [--fixed-value] <name> <value>  'git config unset' [<file-option>] [--all] [--value=<pattern>] [--fixed-value] <name>  'git config rename-section' [<file-option>] <ol…
- `[]`
  > the form `$GIT_DIR/worktrees/<id>/` for other working trees. See  linkgit:git-worktree[1] to learn how to enable  `extensions.worktreeConfig`.  -f <config-file>::  --file <config-file>::  For writing options: write to the specified file rather than the  repository `.git/config`.  +  For reading options: read only from the specified file rather than from all  available files.  +  See also <<FILES>>.  --blob <blob>::  Similar to `--file` but use the given blob instead of a file. E.g.  you can use 'master:.gitmodules' to read values from the file  '.gitmodules' in the master branch. See "SPECIFYI…
- `[]`
  > `extensions.worktreeConfig` is present in $GIT_DIR/config.  You may also provide additional configuration parameters when running any  git command by using the `-c` option. See linkgit:git[1] for details.  Options will be read from all of these files that are available. If the  global or the system-wide configuration files are missing or unreadable they  will be ignored. If the repository configuration file is missing or unreadable,  'git config' will exit with a non-zero error code. An error message is produced  if the file is unreadable, but not if it is missing.  The files are read in the o…
- `['This is the config file, and']`
  > This is the config file, and
- `["a '#' or ';' character indicates"]`
  > a '#' or ';' character indicates
- `['a comment']`
  > a comment  ; core variables  [core]  ; Don't trust file modes  filemode = false  ; Our diff algorithm  [diff]  external = /usr/local/bin/diff-wrapper  renames = true  ; Proxy settings  [core]  gitproxy=proxy-command for kernel.org  gitproxy=default-proxy ; for all the rest  ; HTTP  [http]  sslVerify  [http "https://weak.example.com"]  sslVerify = false  cookieFile = /tmp/cookie.txt  ------------  you can set the filemode to true with  ------------  % git config set core.filemode true  ------------  The hypothetical proxy command entries actually have a postfix to discern  what URL they apply t…

`verdict_relevant:` ______   `notes:` ______

---

## 119. `ff16d8dffe428349`  (python)

**Query** (en / concept): How do class variables and instance variables differ in Python, and how does attribute lookup work?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/classes.rst`

**Claimed section** (unverified): `['Classes', 'Class and Instance Variables']`

**Actual sections in the index:**

- `[]`
  > .. _tut-classes:  *******  Classes  *******  Classes provide a means of bundling data and functionality together. Creating  a new class creates a new *type* of object, allowing new *instances* of that  type to be made. Each class instance can have attributes attached to it for  maintaining its state. Class instances can also have methods (defined by its  class) for modifying its state.  Compared with other programming languages, Python's class mechanism adds classes  with a minimum of new syntax and semantics. It is a mixture of the class  mechanisms found in C++ and Modula-3. Python classes p…
- `[]`
  > time during execution, there are 3 or 4 nested scopes whose namespaces are  directly accessible:  * the innermost scope, which is searched first, contains the local names  * the scopes of any enclosing functions, which are searched starting with the  nearest enclosing scope, contain non-local, but also non-global names  * the next-to-last scope contains the current module's global names  * the outermost scope (searched last) is the namespace containing built-in names  If a name is declared global, then all references and assignments go directly to  the next-to-last scope containing the module'…
- `[]`
  > :meth:`~object.__init__`, like this::  def __init__(self):  self.data = []  When a class defines an :meth:`~object.__init__` method, class instantiation  automatically invokes :meth:`!__init__` for the newly created class instance. So  in this example, a new, initialized instance can be obtained by::  x = MyClass()  Of course, the :meth:`~object.__init__` method may have arguments for greater  flexibility. In that case, arguments given to the class instantiation operator  are passed on to :meth:`!__init__`. For example, ::  >>> class Complex:  ... def __init__(self, realpart, imagpart):  ... s…
- `[]`
  > Clients should use data attributes with care --- clients may mess up invariants  maintained by the methods by stamping on their data attributes. Note that  clients may add data attributes of their own to an instance object without  affecting the validity of the methods, as long as name conflicts are avoided ---  again, a naming convention can save a lot of headaches here.  There is no shorthand for referencing data attributes (or other methods!) from  within methods. I find that this actually increases the readability of methods:  there is no chance of confusing local variables and instance va…
- `['Function defined outside the class']`
  > Function defined outside the class  def f1(self, x, y):  return min(x, x+y)  class C:  f = f1  def g(self):  return 'hello world'  h = g  Now ``f``, ``g`` and ``h`` are all attributes of class :class:`!C` that refer to  function objects, and consequently they are all methods of instances of  :class:`!C` --- ``h`` being exactly equivalent to ``g``. Note that this practice  usually only serves to confuse the reader of a program.  Methods may call other methods by using method attributes of the ``self``  argument::  class Bag:  def __init__(self):  self.data = []  def add(self, x):  self.data.app…
- `['Function defined outside the class']`
  > .. index::  pair: name; mangling  Since there is a valid use-case for class-private members (namely to avoid name  clashes of names with names defined by subclasses), there is limited support for  such a mechanism, called :dfn:`name mangling`. Any identifier of the form  ``__spam`` (at least two leading underscores, at most one trailing underscore)  is textually replaced with ``_classname__spam``, where ``classname`` is the  current class name with leading underscore(s) stripped. This mangling is done  without regard to the syntactic position of the identifier, as long as it  occurs within the…

`verdict_relevant:` ______   `notes:` ______

---

## 120. `52733fd1f2d4e465`  (kubernetes)

**Query** (en / troubleshooting): My pod is failing; how do I determine the reason for the failure?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/debug/debug-application/determine-reason-pod-failure.md`

**Claimed section** (unverified): `['Determine the Reason for Pod Failure']`

**Actual sections in the index:**

- `[]`
  > This page shows how to write and read a Container termination message.  Termination messages provide a way for containers to write  information about fatal events to a location where it can  be easily retrieved and surfaced by tools like dashboards  and monitoring software. In most cases, information that you  put in a termination message should also be written to  the general  [Kubernetes logs](/docs/concepts/cluster-administration/logging/).
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}
- `['{{% heading "prerequisites" %}}', 'Writing and reading a termination message']`
  > Writing and reading a termination message  In this exercise, you create a Pod that runs one container.  The manifest for that Pod specifies a command that runs when the container starts:  {{% code_sample file="debug/termination.yaml" %}}  1. Create a Pod based on the YAML configuration file:  kubectl apply -f https://k8s.io/examples/debug/termination.yaml  In the YAML file, in the `command` and `args` fields, you can see that the  container sleeps for 10 seconds and then writes "Sleep expired" to  the `/dev/termination-log` file. After the container writes  the "Sleep expired" message, it term…
- `['{{% heading "prerequisites" %}}', 'Customizing the termination message']`
  > Customizing the termination message  Kubernetes retrieves termination messages from the termination message file  specified in the `terminationMessagePath` field of a Container, which has a default  value of `/dev/termination-log`. By customizing this field, you can tell Kubernetes  to use a different file. Kubernetes use the contents from the specified file to  populate the Container's status message on both success and failure.  The termination message is intended to be brief final status, such as an assertion failure message.  The kubelet truncates messages that are longer than 4096 bytes.…
- `['{{% heading "prerequisites" %}}', '{{% heading "whatsnext" %}}']`
  > {{% heading "whatsnext" %}}  * See the `terminationMessagePath` field in  [Container](/docs/reference/generated/kubernetes-api/{{< param "version" >}}/#container-v1-core).  * See [ImagePullBackOff](/docs/concepts/containers/images/#imagepullbackoff) in [Images](/docs/concepts/containers/images/).  * Learn about [retrieving logs](/docs/concepts/cluster-administration/logging/).  * Learn about [Go templates](https://pkg.go.dev/text/template).  * Learn about [Pod status](/docs/tasks/debug/debug-application/debug-init-containers/#understanding-pod-status) and [Pod phase](/docs/concepts/workloads/p…

`verdict_relevant:` ______   `notes:` ______

---

## 121. `00d0ff7fb8f88612`  (go)

**Query** (zh / code_api): Go 的内建函数里，make 和 append 分别有什么用途？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html`

**Claimed section** (unverified): `['The Go Programming Language Specification', 'Built-in functions']`

**Actual sections in the index:**

- `[]`
  > Introduction  This is the reference manual for the Go programming language.  For more information and other documents, see go.dev.  Go is a general-purpose language designed with systems programming  in mind. It is strongly typed and garbage-collected and has explicit  support for concurrent programming. Programs are constructed from  packages, whose properties allow efficient management of  dependencies.  The syntax is compact and simple to parse, allowing for easy analysis  by automatic tools such as integrated development environments.  Notation  The syntax is specified using a  variant  of…
- `[]`
  > - | -= |= || < <= [ ]  * ^ *= ^= <- > >= { }  / << /= <<= ++ = := , ;  % >> %= >>= -- ! ... . :  &^ &^= ~  Integer literals  An integer literal is a sequence of digits representing an  integer constant.  An optional prefix sets a non-decimal base: 0b or 0B  for binary, 0, 0o, or 0O for octal,  and 0x or 0X for hexadecimal  [Go 1.13].  A single 0 is considered a decimal zero.  In hexadecimal literals, letters a through f  and A through F represent values 10 through 15.  For readability, an underscore character _ may appear after  a base prefix or between successive digits; such underscores do n…
- `[]`
  > In each case the value of the literal is the value represented by  the digits in the corresponding base.  Although these representations all result in an integer, they have  different valid ranges. Octal escapes must represent a value between  0 and 255 inclusive. Hexadecimal escapes satisfy this condition  by construction. The escapes \u and \U  represent Unicode code points so within them some values are illegal,  in particular those above 0x10FFFF and surrogate halves.  After a backslash, certain single-character escapes represent special values:  \a U+0007 alert or bell  \b U+0008 backspac…
- `[]`
  > respectively, depending on whether it is a boolean, rune, integer, floating-point,  complex, or string constant.  Implementation restriction: Although numeric constants have arbitrary  precision in the language, a compiler may implement them using an  internal representation with limited precision. That said, every  implementation must:  Represent integer constants with at least 256 bits.  Represent floating-point constants, including the parts of  a complex constant, with a mantissa of at least 256 bits  and a signed binary exponent of at least 16 bits.  Give an error if unable to represent a…
- `[]`
  > The length of a string s can be discovered using  the built-in function len.  The length is a compile-time constant if the string is a constant.  A string's bytes can be accessed by integer indices  0 through len(s)-1.  It is illegal to take the address of such an element; if  s[i] is the i'th byte of a  string, &s[i] is invalid.  Array types  An array is a numbered sequence of elements of a single  type, called the element type.  The number of elements is called the length of the array and is never negative.  ArrayType = "[" ArrayLength "]" ElementType .  ArrayLength = Expression .  ElementTy…
- `[]`
  > T, promoted methods are included in the method set of the struct as follows:  If S contains an embedded field T,  the method sets of S  and *S both include promoted methods with receiver  T. The method set of *S also  includes promoted methods with receiver *T.  If S contains an embedded field *T,  the method sets of S and *S both  include promoted methods with receiver T or  *T.  A field declaration may be followed by an optional string literal tag,  which becomes an attribute for all the fields in the corresponding  field declaration. An empty tag string is equivalent to an absent tag.  The…

`verdict_relevant:` ______   `notes:` ______

---

## 122. `bcb764fa0a18f464`  (git)

**Query** (en / code_api): How do pattern rules in a .gitattributes file match paths, and what attributes can they set?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/gitattributes.adoc`

**Claimed section** (unverified): `['gitattributes', 'DESCRIPTION']`

**Actual sections in the index:**

- `[]`
  > gitattributes(5)  ================  NAME  ----  gitattributes - Defining attributes per path  SYNOPSIS  --------  $GIT_DIR/info/attributes, .gitattributes  DESCRIPTION  -----------  A `gitattributes` file is a simple text file that gives  `attributes` to pathnames.  Each line in `gitattributes` file is of form:  pattern attr1 attr2 ...  That is, a pattern followed by an attributes list,  separated by whitespaces. Leading and trailing whitespaces are  ignored. Lines that begin with '#' are ignored. Patterns  that begin with a double quote are quoted in C style.  When the pattern matches the pat…
- `[]`
  > This setting converts the file's line endings in the working  directory to CRLF when the file is checked out.  Set to string value "lf"::  This setting uses the same line endings in the working directory as  in the index when the file is checked out.  Unspecified::  If the `eol` attribute is unspecified for a file, its line endings  in the working directory are determined by the `core.autocrlf` or  `core.eol` configuration variable (see the definitions of those  options in linkgit:git-config[1]). If `text` is set but neither of  those variables is, the default is `eol=crlf` on Windows and  `eo…
- `[]`
  > Git operations (e.g 'git checkout' or 'git add').  Use the `working-tree-encoding` attribute only if you cannot store a file  in UTF-8 encoding and if you want Git to be able to process the content  as text.  As an example, use the following attributes if your '*.ps1' files are  UTF-16 encoded with byte order mark (BOM) and you want Git to perform  automatic line ending conversion based on your platform.  ------------------------  *.ps1 text working-tree-encoding=UTF-16  ------------------------  Use the following attributes if your '*.ps1' files are UTF-16 little  endian encoded without BOM a…
- `[]`
  > When Git encounters the first file that needs to be cleaned or smudged,  it starts the filter and performs the handshake. In the handshake, the  welcome message sent by Git is "git-filter-client", only version 2 is  supported, and the supported capabilities are "clean", "smudge", and  "delay".  Afterwards Git sends a list of "key=value" pairs terminated with  a flush packet. The list will contain at least the filter command  (based on the supported capabilities) and the pathname of the file  to filter relative to the repository root. Right after the flush packet  Git sends the content split in…
- `[]`
  > Merging branches with differing checkin/checkout attributes  ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^  If you have added attributes to a file that cause the canonical  repository format for that file to change, such as adding a  clean/smudge filter or text/eol/ident attributes, merging anything  where the attribute is not in place would normally cause merge  conflicts.  To prevent these unnecessary merge conflicts, Git can be told to run a  virtual check-out and check-in of all three stages of each file that  needs a three-way content merge, by setting the `merge.renormalize…
- `[]`
  > - `css` suitable for cascading style sheets.  - `dts` suitable for devicetree (DTS) files.  - `elixir` suitable for source code in the Elixir language.  - `fortran` suitable for source code in the Fortran language.  - `fountain` suitable for Fountain documents.  - `golang` suitable for source code in the Go language.  - `html` suitable for HTML/XHTML documents.  - `java` suitable for source code in the Java language.  - `kotlin` suitable for source code in the Kotlin language.  - `markdown` suitable for Markdown documents.  - `matlab` suitable for source code in the MATLAB and Octave languages…

`verdict_relevant:` ______   `notes:` ______

---

## 123. `ac396700175523f1`  (kubernetes)

**Query** (zh / troubleshooting): DNS 解析在集群里失败，怎么定位问题？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/administer-cluster/dns-debugging-resolution.md`

**Claimed section** (unverified): `['Debugging DNS Resolution']`

**Actual sections in the index:**

- `[]`
  > This page provides hints on diagnosing DNS problems.
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}  Your cluster must be configured to use the CoreDNS  {{< glossary_tooltip text="addon" term_id="addons" >}} or its precursor,  kube-dns.  {{% version-check %}}
- `['{{% heading "prerequisites" %}}', 'Create a simple Pod to use as a test environment']`
  > Create a simple Pod to use as a test environment  {{% code_sample file="admin/dns/dnsutils.yaml" %}}  {{< note >}}  This example creates a pod in the `default` namespace. DNS name resolution for  services depends on the namespace of the pod. For more information, review  [DNS for Services and Pods](/docs/concepts/services-networking/dns-pod-service/#what-things-get-dns-names).  {{< /note >}}  Use that manifest to create a Pod:  kubectl apply -f https://k8s.io/examples/admin/dns/dnsutils.yaml  pod/dnsutils created  …and verify its status:  kubectl get pods dnsutils  NAME       READY     STATUS…
- `['{{% heading "prerequisites" %}}', 'Create a simple Pod to use as a test environment', 'Check the local DNS configuration first']`
  > Check the local DNS configuration first  Take a look inside the resolv.conf file.  (See [Customizing DNS Service](/docs/tasks/administer-cluster/dns-custom-nameservers) and  [Known issues](#known-issues) below for more information)  kubectl exec -ti dnsutils -- cat /etc/resolv.conf  Verify that the search path and name server are set up like the following  (note that search path may vary for different cloud providers):  search default.svc.cluster.local svc.cluster.local cluster.local google.internal c.gce_project_id.internal  nameserver 10.0.0.10  options ndots:5  Errors such as the following…
- `['{{% heading "prerequisites" %}}', 'Create a simple Pod to use as a test environment', 'Check if the DNS pod is running']`
  > Check if the DNS pod is running  Use the `kubectl get pods` command to verify that the DNS pod is running.  kubectl get pods --namespace=kube-system -l k8s-app=kube-dns  NAME                       READY     STATUS    RESTARTS   AGE  ...  coredns-7b96bf9f76-5hsxb   1/1       Running   0           1h  coredns-7b96bf9f76-mvmmt   1/1       Running   0           1h  ...  {{< note >}}  The value for label `k8s-app` is `kube-dns` for both CoreDNS and kube-dns deployments.  {{< /note >}}  If you see that no CoreDNS Pod is running or that the Pod has failed/completed,  the DNS add-on may not be deploye…
- `['{{% heading "prerequisites" %}}', 'Create a simple Pod to use as a test environment', 'Check for errors in the DNS pod']`
  > Check for errors in the DNS pod  Use the `kubectl logs` command to see logs for the DNS containers.  For CoreDNS:  kubectl logs --namespace=kube-system -l k8s-app=kube-dns  Here is an example of a healthy CoreDNS log:  .:53  2018/08/15 14:37:17 [INFO] CoreDNS-1.2.2  2018/08/15 14:37:17 [INFO] linux/amd64, go1.10.3, 2e322f6  CoreDNS-1.2.2  linux/amd64, go1.10.3, 2e322f6  2018/08/15 14:37:17 [INFO] plugin/reload: Running configuration MD5 = 24e6c59e83ce706f07bcc82c31b1ea1c  See if there are any suspicious or unexpected messages in the logs.

`verdict_relevant:` ______   `notes:` ______

---

## 124. `f105a927f70b2ed7`  (postgresql)

**Query** (zh / command): 怎么备份 PostgreSQL 数据库，SQL 转储和文件系统备份有什么区别？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/backup.sgml`

**Claimed section** (unverified): `['Backup and Restore']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/backup.sgml -->  <chapter id="backup">  <title>Backup and Restore</title>  <indexterm zone="backup"><primary>backup</primary></indexterm>  <para>  As with everything that contains valuable data, <productname>PostgreSQL</productname>  databases should be backed up regularly. While the procedure is  essentially simple, it is important to have a clear understanding of  the underlying techniques and assumptions.  </para>  <para>  There are three fundamentally different approaches to backing up  <productname>PostgreSQL</productname> data:  <itemizedlist>  <listitem><para><acronym>…
- `[]`
  > After restoring a backup, it is wise to run <link  linkend="sql-analyze"><command>ANALYZE</command></link> on each  database so the query optimizer has useful statistics;  see <xref linkend="vacuum-for-statistics"/>  and <xref linkend="autovacuum"/> for more information.  For more advice on how to load large amounts of data  into <productname>PostgreSQL</productname> efficiently, refer to <xref  linkend="populate"/>.  </para>  </sect2>  <sect2 id="backup-dump-all">  <title>Using <application>pg_dumpall</application></title>  <para>  <application>pg_dump</application> dumps only a single databa…
- `[]`
  > <para>  An alternative file-system backup approach is to make a  <quote>consistent snapshot</quote> of the data directory, if the  file system supports that functionality (and you are willing to  trust that it is implemented correctly). The typical procedure is  to make a <quote>frozen snapshot</quote> of the volume containing the  database, then copy the whole data directory (not just parts, see  above) from the snapshot to a backup device, then release the frozen  snapshot. This will work even while the database server is running.  However, a backup created in this way saves  the database fi…
- `[]`
  > first base backup. Accordingly, we first discuss the mechanics of  archiving WAL files.  </para>  <sect2 id="backup-archiving-wal">  <title>Setting Up WAL Archiving</title>  <para>  In an abstract sense, a running <productname>PostgreSQL</productname> system  produces an indefinitely long sequence of WAL records. The system  physically divides this sequence into WAL <firstterm>segment  files</firstterm>, which are normally 16MB apiece (although the segment size  can be altered during <application>initdb</application>). The segment  files are given numeric names that reflect their position in t…
- `[]`
  > the right exit status is returned. (In particular, GNU <command>cp</command>  will return status zero when <option>-i</option> is used and the target file  already exists, which is <emphasis>not</emphasis> the desired behavior.)  </para>  <para>  While designing your archiving setup, consider what will happen if  the archive command or library fails repeatedly because some aspect requires  operator intervention or the archive runs out of space. For example, this  could occur if you write to tape without an autochanger; when the tape  fills, nothing further can be archived until the tape is swa…
- `[]`
  > the starting and ending times and WAL segments of the backup.  If you used the label to identify the associated dump file,  then the archived history file is enough to tell you which dump file to  restore.  </para>  <para>  Since you have to keep around all the archived WAL files back to your  last base backup, the interval between base backups should usually be  chosen based on how much storage you want to expend on archived WAL  files. You should also consider how long you are prepared to spend  recovering, if recovery should be necessary &mdash; the system will have to  replay all those WAL…

`verdict_relevant:` ______   `notes:` ______

---

## 125. `ccb3fe183714fb8d`  (python)

**Query** (en / command): How do you pass arguments to a Python script, and where can the script access them?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/interpreter.rst`

**Claimed section** (unverified): `['Using the Python Interpreter', 'Argument Passing']`

**Actual sections in the index:**

- `[]`
  > .. _tut-using:  ****************************  Using the Python Interpreter  ****************************  .. _tut-invoking:  Invoking the Interpreter  ========================  The Python interpreter is usually installed as |usr_local_bin_python_x_dot_y_literal|  on those machines where it is available; putting :file:`/usr/local/bin` in your  Unix shell's search path makes it possible to start it by typing the command:  .. code-block:: text  python3.16  to the shell. [#]_ Since the choice of the directory where the interpreter lives  is an installation option, other places are possible; check…
- `['-*- coding: encoding -*-']`
  > -*- coding: encoding -*-  where *encoding* is one of the valid :mod:`codecs` supported by Python.  For example, to declare that Windows-1252 encoding is to be used, the first  line of your source code file should be::
- `['-*- coding: cp1252 -*-']`
  > -*- coding: cp1252 -*-  One exception to the *first line* rule is when the source code starts with a  :ref:`UNIX "shebang" line <tut-scripts>`. In this case, the encoding  declaration should be added as the second line of the file. For example::
- `['!/usr/bin/env python3']`
  > !/usr/bin/env python3
- `['-*- coding: cp1252 -*-']`
  > -*- coding: cp1252 -*-  .. rubric:: Footnotes  .. [#] On Unix, the Python 3.x interpreter is by default not installed with the  executable named ``python``, so that it does not conflict with a  simultaneously installed Python 2.x executable.

`verdict_relevant:` ______   `notes:` ______

---

## 126. `e4d88e45af467b98`  (kubernetes)

**Query** (en / code_api): What are Custom Resources and how do they extend the Kubernetes API?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/extend-kubernetes/api-extension/custom-resources.md`

**Claimed section** (unverified): `['Custom Resources']`

**Actual sections in the index:**

- `[]`
  > *Custom resources* are extensions of the Kubernetes API. This page discusses when to add a custom  resource to your Kubernetes cluster and when to use a standalone service. It describes the two  methods for adding custom resources and how to choose between them.
- `['Custom resources']`
  > Custom resources  A *resource* is an endpoint in the [Kubernetes API](/docs/concepts/overview/kubernetes-api/) that  stores a collection of {{< glossary_tooltip text="API objects" term_id="object" >}}  of a certain kind; for example, the built-in *pods* resource contains a collection of Pod objects.  A *custom resource* is an extension of the Kubernetes API that is not necessarily available in a default  Kubernetes installation. It represents a customization of a particular Kubernetes installation. However,  many core Kubernetes functions are now built using custom resources, making Kubernetes…
- `['Custom resources', 'Custom controllers']`
  > Custom controllers  On their own, custom resources let you store and retrieve structured data.  When you combine a custom resource with a *custom controller*, custom resources  provide a true _declarative API_.  The Kubernetes [declarative API](/docs/concepts/overview/kubernetes-api/)  enforces a separation of responsibilities. You declare the desired state of  your resource. The Kubernetes controller keeps the current state of Kubernetes  objects in sync with your declared desired state. This is in contrast to an  imperative API, where you *instruct* a server what to do.  You can deploy and u…
- `['Custom resources', 'Should I add a custom resource to my Kubernetes cluster?']`
  > Should I add a custom resource to my Kubernetes cluster?  When creating a new API, consider whether to  [aggregate your API with the Kubernetes cluster APIs](/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/)  or let your API stand alone.  | Consider API aggregation if: | Prefer a stand-alone API if: |  | ---------------------------- | ---------------------------- |  | Your API is [Declarative](#declarative-apis). | Your API does not fit the [Declarative](#declarative-apis) model. |  | You want your new types to be readable and writable using `kubectl`.| `kubectl` support i…
- `['Custom resources', 'Should I add a custom resource to my Kubernetes cluster?', 'Declarative APIs']`
  > Declarative APIs  In a Declarative API, typically:  - Your API consists of a relatively small number of relatively small objects (resources).  - The objects define configuration of applications or infrastructure.  - The objects are updated relatively infrequently.  - Humans often need to read and write the objects.  - The main operations on the objects are CRUD-y (creating, reading, updating and deleting).  - Transactions across objects are not required: the API represents a desired state, not an exact state.  Imperative APIs are not declarative.  Signs that your API might not be declarative i…
- `['Custom resources', 'Should I use a ConfigMap or a custom resource?']`
  > Should I use a ConfigMap or a custom resource?  Use a ConfigMap if any of the following apply:  * There is an existing, well-documented configuration file format, such as a `mysql.cnf` or  `pom.xml`.  * You want to put the entire configuration into one key of a ConfigMap.  * The main use of the configuration file is for a program running in a Pod on your cluster to  consume the file to configure itself.  * Consumers of the file prefer to consume via file in a Pod or environment variable in a pod,  rather than the Kubernetes API.  * You want to perform rolling updates via Deployment, etc., when…

`verdict_relevant:` ______   `notes:` ______

---

## 127. `0bfd69f3e95a6afc`  (postgresql)

**Query** (zh / config): 怎么开启连续归档并配置时间点恢复（PITR）？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/backup.sgml`

**Claimed section** (unverified): `['Backup and Restore', 'Continuous Archiving and Point-in-Time Recovery (PITR)']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/backup.sgml -->  <chapter id="backup">  <title>Backup and Restore</title>  <indexterm zone="backup"><primary>backup</primary></indexterm>  <para>  As with everything that contains valuable data, <productname>PostgreSQL</productname>  databases should be backed up regularly. While the procedure is  essentially simple, it is important to have a clear understanding of  the underlying techniques and assumptions.  </para>  <para>  There are three fundamentally different approaches to backing up  <productname>PostgreSQL</productname> data:  <itemizedlist>  <listitem><para><acronym>…
- `[]`
  > After restoring a backup, it is wise to run <link  linkend="sql-analyze"><command>ANALYZE</command></link> on each  database so the query optimizer has useful statistics;  see <xref linkend="vacuum-for-statistics"/>  and <xref linkend="autovacuum"/> for more information.  For more advice on how to load large amounts of data  into <productname>PostgreSQL</productname> efficiently, refer to <xref  linkend="populate"/>.  </para>  </sect2>  <sect2 id="backup-dump-all">  <title>Using <application>pg_dumpall</application></title>  <para>  <application>pg_dump</application> dumps only a single databa…
- `[]`
  > <para>  An alternative file-system backup approach is to make a  <quote>consistent snapshot</quote> of the data directory, if the  file system supports that functionality (and you are willing to  trust that it is implemented correctly). The typical procedure is  to make a <quote>frozen snapshot</quote> of the volume containing the  database, then copy the whole data directory (not just parts, see  above) from the snapshot to a backup device, then release the frozen  snapshot. This will work even while the database server is running.  However, a backup created in this way saves  the database fi…
- `[]`
  > first base backup. Accordingly, we first discuss the mechanics of  archiving WAL files.  </para>  <sect2 id="backup-archiving-wal">  <title>Setting Up WAL Archiving</title>  <para>  In an abstract sense, a running <productname>PostgreSQL</productname> system  produces an indefinitely long sequence of WAL records. The system  physically divides this sequence into WAL <firstterm>segment  files</firstterm>, which are normally 16MB apiece (although the segment size  can be altered during <application>initdb</application>). The segment  files are given numeric names that reflect their position in t…
- `[]`
  > the right exit status is returned. (In particular, GNU <command>cp</command>  will return status zero when <option>-i</option> is used and the target file  already exists, which is <emphasis>not</emphasis> the desired behavior.)  </para>  <para>  While designing your archiving setup, consider what will happen if  the archive command or library fails repeatedly because some aspect requires  operator intervention or the archive runs out of space. For example, this  could occur if you write to tape without an autochanger; when the tape  fills, nothing further can be archived until the tape is swa…
- `[]`
  > the starting and ending times and WAL segments of the backup.  If you used the label to identify the associated dump file,  then the archived history file is enough to tell you which dump file to  restore.  </para>  <para>  Since you have to keep around all the archived WAL files back to your  last base backup, the interval between base backups should usually be  chosen based on how much storage you want to expend on archived WAL  files. You should also consider how long you are prepared to spend  recovering, if recovery should be necessary &mdash; the system will have to  replay all those WAL…

`verdict_relevant:` ______   `notes:` ______

---

## 128. `bdcfb3a6f3a1c9bd`  (go)

**Query** (zh / concept): Go 为什么用 GODEBUG 这种开关机制维持向后兼容，而不是直接删除旧行为？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `['Go, Backwards Compatibility, and GODEBUG', 'Introduction']`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 129. `f141c35c93ee4cd7`  (kubernetes)

**Query** (zh / troubleshooting): kubectl 命令行为异常，怎么排查？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/debug/debug-cluster/troubleshoot-kubectl.md`

**Claimed section** (unverified): `['Troubleshooting kubectl']`

**Actual sections in the index:**

- `[]`
  > This documentation is about investigating and diagnosing  {{<glossary_tooltip text="kubectl" term_id="kubectl">}} related issues.  If you encounter issues accessing `kubectl` or connecting to your cluster, this  document outlines various common scenarios and potential solutions to help  identify and address the likely cause.
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  * You need to have a Kubernetes cluster.  * You also need to have `kubectl` installed - see [install tools](/docs/tasks/tools/#kubectl)
- `['{{% heading "prerequisites" %}}', 'Verify kubectl setup']`
  > Verify kubectl setup  Make sure you have installed and configured `kubectl` correctly on your local machine.  Check the `kubectl` version to ensure it is up-to-date and compatible with your cluster.  Check kubectl version:  kubectl version  You'll see a similar output:  Client Version: version.Info{Major:"1", Minor:"27", GitVersion:"v1.27.4",GitCommit:"fa3d7990104d7c1f16943a67f11b154b71f6a132", GitTreeState:"clean",BuildDate:"2023-07-19T12:20:54Z", GoVersion:"go1.20.6", Compiler:"gc", Platform:"linux/amd64"}  Kustomize Version: v5.0.1  Server Version: version.Info{Major:"1", Minor:"27", GitVer…
- `['{{% heading "prerequisites" %}}', 'Check kubeconfig']`
  > Check kubeconfig  The `kubectl` requires a `kubeconfig` file to connect to a Kubernetes cluster. The  `kubeconfig` file is usually located under the `~/.kube/config` directory. Make sure  that you have a valid `kubeconfig` file. If you don't have a `kubeconfig` file, you can  obtain it from your Kubernetes administrator, or you can copy it from your Kubernetes  control plane's `/etc/kubernetes/admin.conf` directory. If you have deployed your  Kubernetes cluster on a cloud platform and lost your `kubeconfig` file, you can  re-generate it using your cloud provider's tools. Refer the cloud provid…
- `['{{% heading "prerequisites" %}}', 'Check VPN connectivity']`
  > Check VPN connectivity  If you are using a Virtual Private Network (VPN) to access your Kubernetes cluster,  make sure that your VPN connection is active and stable. Sometimes, VPN disconnections  can lead to connection issues with the cluster. Reconnect to the VPN and try accessing  the cluster again.
- `['{{% heading "prerequisites" %}}', 'Authentication and authorization']`
  > Authentication and authorization  If you are using the token based authentication and the kubectl is returning an error  regarding the authentication token or authentication server address, validate the  Kubernetes authentication token and the authentication server address are configured  properly.  If kubectl is returning an error regarding the authorization, make sure that you are  using the valid user credentials. And you have the permission to access the resource  that you have requested.

`verdict_relevant:` ______   `notes:` ______

---

## 130. `6d36b22231402339`  (go)

**Query** (zh / command): 在 amd64 的 Go 汇编里访问 g 和 m 指针时，用 MOVQ 还是 MOVL，为什么？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", '64-bit Intel 386 (a.k.a. amd64)']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 131. `1aa8c0ca1d9a5c9b`  (python)

**Query** (zh / code_api): 怎么用 argparse 给脚本添加带不同类型的命令行参数？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/argparse.rst`

**Claimed section** (unverified): `['ArgumentParser objects']`

**Actual sections in the index:**

- `[]`
  > :mod:`!argparse` --- Parser for command-line options, arguments and subcommands  ================================================================================  .. module:: argparse  :synopsis: Command-line option and argument parsing library.  .. versionadded:: 3.2  **Source code:** :source:`Lib/argparse.py`  .. note::  While :mod:`!argparse` is the default recommended standard library module  for implementing basic command line applications, authors with more  exacting requirements for exactly how their command line applications  behave may find it doesn't provide the necessary level of co…
- `[]`
  > ``description=`` keyword argument. This argument gives a brief description of  what the program does and how it works. In help messages, the description is  displayed between the command-line usage string and the help messages for the  various arguments.  By default, the description will be line-wrapped so that it fits within the  given space. To change this behavior, see the formatter_class_ argument.  epilog  ^^^^^^  Some programs like to display additional description of the program after the  description of the arguments. Such text can be specified using the ``epilog=``  argument to :class…
- `[]`
  > and ``"strict"``) to the :term:`filesystem encoding and error handler`.  Arguments file should be encoded in UTF-8 instead of ANSI Codepage on Windows.  argument_default  ^^^^^^^^^^^^^^^^  Generally, argument defaults are specified either by passing a default to  :meth:`~ArgumentParser.add_argument` or by calling the  :meth:`~ArgumentParser.set_defaults` methods with a specific set of name-value  pairs. Sometimes however, it may be useful to specify a single parser-wide  default for arguments. This can be accomplished by passing the  ``argument_default=`` keyword argument to :class:`ArgumentPa…
- `[]`
  > * const_ - A constant value required by some action_ and nargs_ selections.  * default_ - The value produced if the argument is absent from the  command line and if it is absent from the namespace object.  * type_ - The type to which the command-line argument should be converted.  * choices_ - A sequence of the allowable values for the argument.  * required_ - Whether or not the command-line option may be omitted  (optionals only).  * help_ - A brief description of what the argument does.  * metavar_ - A name for the argument in usage messages.  * dest_ - The name of the attribute to be added…
- `[]`
  > ...  >>> parser = argparse.ArgumentParser()  >>> parser.add_argument('--foo', action=FooAction)  >>> parser.add_argument('bar', action=FooAction)  >>> args = parser.parse_args('1 --foo 2'.split())  Namespace(bar=None, foo=None) '1' None  Namespace(bar='1', foo=None) '2' '--foo'  >>> args  Namespace(bar='1', foo='2')  For more details, see :class:`Action`.  .. _nargs:  nargs  ^^^^^  :class:`ArgumentParser` objects usually associate a single command-line argument with a  single action to be taken. The ``nargs`` keyword argument associates a  different number of command-line arguments with a sing…
- `[]`
  > the name of a registered type (see :meth:`~ArgumentParser.register`)  If the function raises :exc:`ArgumentTypeError`, :exc:`TypeError`, or  :exc:`ValueError`, the exception is caught and a nicely formatted error  message is displayed. Other exception types are not handled.  Common built-in types and functions can be used as type converters:  .. testcode::  import argparse  import pathlib  parser = argparse.ArgumentParser()  parser.add_argument('count', type=int)  parser.add_argument('distance', type=float)  parser.add_argument('street', type=ascii)  parser.add_argument('code_point', type=ord)…

`verdict_relevant:` ______   `notes:` ______

---

## 132. `c8f8ad521e7de6a7`  (postgresql)

**Query** (zh / troubleshooting): 主备切换后应用在备库上执行写操作失败，常见原因是什么？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/high-availability.sgml`

**Claimed section** (unverified): `['High Availability, Load Balancing, and Replication', 'Hot Standby']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/high-availability.sgml -->  <chapter id="high-availability">  <title>High Availability, Load Balancing, and Replication</title>  <indexterm><primary>high availability</primary></indexterm>  <indexterm><primary>failover</primary></indexterm>  <indexterm><primary>replication</primary></indexterm>  <indexterm><primary>load balancing</primary></indexterm>  <indexterm><primary>clustering</primary></indexterm>  <indexterm><primary>data partitioning</primary></indexterm>  <para>  Database servers can work together to allow a second server to  take over quickly if the primary server…
- `[]`
  > the primary server sends data changes (typically) asynchronously to the  standby servers. Standby servers can answer queries while the primary is  running, and may allow some local data changes or write activity. This  form of replication is often used for offloading large analytical or data  warehouse queries.  </para>  <para>  <productname>Slony-I</productname> is an example of this type of  replication, with per-table granularity, and support for multiple standby  servers. Because it updates the standby server asynchronously (in  batches), there is possible data loss during fail over.  </pa…
- `[]`
  > is open source and easily extended, a number of companies have  taken <productname>PostgreSQL</productname> and created commercial  closed-source solutions with unique failover, replication, and load  balancing capabilities. These are not discussed here.  </para>  </sect1>  <sect1 id="warm-standby">  <title>Log-Shipping Standby Servers</title>  <para>  Continuous archiving can be used to create a <firstterm>high  availability</firstterm> (HA) cluster configuration with one or more  <firstterm>standby servers</firstterm> ready to take over operations if the  primary server fails. This capabilit…
- `[]`
  > restore the file from the archive again. This loop of retries from the  archive, <filename>pg_wal</filename>, and via streaming replication goes on until the server  is stopped or is promoted.  </para>  <para>  Standby mode is exited and the server switches to normal operation  when <command>pg_ctl promote</command> is run, or  <function>pg_promote()</function> is called. Before failover,  any WAL immediately available in the archive or in <filename>pg_wal</filename>  will be restored, but no attempt is made to connect to the primary.  </para>  </sect2>  <sect2 id="preparing-primary-for-standb…
- `[]`
  > successfully, you will see a <literal>walreceiver</literal> in the standby, and  a corresponding <literal>walsender</literal> process in the primary.  </para>  <sect3 id="streaming-replication-authentication">  <title>Authentication</title>  <para>  It is very important that the access privileges for replication be set up  so that only trusted users can read the WAL stream, because it is  easy to extract privileged information from it. Standby servers must  authenticate to the primary as an account that has the  <literal>REPLICATION</literal> privilege or a superuser. It is  recommended to cre…
- `['Allow the user "foo" from host 192.168.1.100 to connect to the primary']`
  > Allow the user "foo" from host 192.168.1.100 to connect to the primary

`verdict_relevant:` ______   `notes:` ______

---

## 133. `76e9613745667458`  (python)

**Query** (en / command): What command creates a new virtual environment, and how do you install packages into it with pip?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/tutorial/venv.rst`

**Claimed section** (unverified): `['Virtual Environments and Packages', 'Creating Virtual Environments']`

**Actual sections in the index:**

- `[]`
  > .. _tut-venv:  *********************************  Virtual Environments and Packages  *********************************  Introduction  ============  Python applications will often use packages and modules that don't  come as part of the standard library. Applications will sometimes  need a specific version of a library, because the application may  require that a particular bug has been fixed or the application may be  written using an obsolete version of the library's interface.  This means it may not be possible for one Python installation to meet  the requirements of every application. If ap…
- `[]`
  > ``pip`` has many more options. Consult the :ref:`installing-index`  guide for complete documentation for ``pip``. When you've written  a package and want to make it available on the Python Package Index,  consult the `Python packaging user guide`_.  .. _Python Packaging User Guide: https://packaging.python.org/en/latest/tutorials/packaging-projects/

`verdict_relevant:` ______   `notes:` ______

---

## 134. `1a73e1c1bb136b5a`  (postgresql)

**Query** (zh / code_api): PostgreSQL 的 JSON 和 JSONB 类型有什么区别？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/json.sgml`

**Claimed section** (unverified): `['JSON Types']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/json.sgml -->  <sect1 id="datatype-json">  <title><acronym>JSON</acronym> Types</title>  <indexterm zone="datatype-json">  <primary>JSON</primary>  </indexterm>  <indexterm zone="datatype-json">  <primary>JSONB</primary>  </indexterm>  <para>  JSON data types are for storing JSON (JavaScript Object Notation)  data, as specified in <ulink url="https://datatracker.ietf.org/doc/html/rfc7159">RFC  7159</ulink>. Such data can also be stored as <type>text</type>, but  the JSON data types have the advantage of enforcing that each  stored value is valid according to the JSON rules. T…
- `[]`
  > As previously stated, when a JSON value is input and then printed without  any additional processing, <type>json</type> outputs the same text that was  input, while <type>jsonb</type> does not preserve semantically-insignificant  details such as whitespace. For example, note the differences here:  <programlisting>  SELECT '{"bar": "baz", "balance": 7.77, "active":false}'::json;  json  -------------------------------------------------  {"bar": "baz", "balance": 7.77, "active":false}  (1 row)  SELECT '{"bar": "baz", "balance": 7.77, "active":false}'::jsonb;  jsonb  ------------------------------…
- `[]`
  > but that approach is less flexible, and often less efficient as well.  </para>  <para>  On the other hand, the JSON existence operator is not nested: it will  only look for the specified key or array element at top level of the  JSON value.  </para>  </tip>  <para>  The various containment and existence operators, along with all other  JSON operators and functions are documented  in <xref linkend="functions-json"/>.  </para>  </sect2>  <sect2 id="json-indexing">  <title><type>jsonb</type> Indexing</title>  <indexterm>  <primary>jsonb</primary>  <secondary>indexes on</secondary>  </indexterm>…
- `[]`
  > that it produces no index entries for JSON structures not containing  any values, such as <literal>{"a": {}}</literal>. If a search for  documents containing such a structure is requested, it will require a  full-index scan, which is quite slow. <literal>jsonb_path_ops</literal> is  therefore ill-suited for applications that often perform such searches.  </para>  <para>  <type>jsonb</type> also supports <literal>btree</literal> and <literal>hash</literal>  indexes. These are usually useful only if it's important to check  equality of complete JSON documents.  The <literal>btree</literal> order…
- `[]`
  > The semantics of SQL/JSON path predicates and operators generally follow SQL.  At the same time, to provide a natural way of working with JSON data,  SQL/JSON path syntax uses some JavaScript conventions:  </para>  <itemizedlist>  <listitem>  <para>  Dot (<literal>.</literal>) is used for member access.  </para>  </listitem>  <listitem>  <para>  Square brackets (<literal>[]</literal>) are used for array access.  </para>  </listitem>  <listitem>  <para>  SQL/JSON arrays are 0-relative, unlike regular SQL arrays that start from 1.  </para>  </listitem>  </itemizedlist>  <para>  Numeric literals…

`verdict_relevant:` ______   `notes:` ______

---

## 135. `a365f336e8d1cf1a`  (go)

**Query** (zh / command): amd64 汇编里 BP 寄存器是调用者保存还是被调用者保存？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/asm.html`

**Claimed section** (unverified): `["A Quick Guide to Go's Assembler", '64-bit Intel 386 (a.k.a. amd64)']`

**Actual sections in the index:**

- `[]`
  > A Quick Guide to Go's Assembler  This document is a quick outline of the unusual form of assembly language used by the gc Go compiler.  The document is not comprehensive.  The assembler is based on the input style of the Plan 9 assemblers, which is documented in detail  elsewhere.  If you plan to write assembly language, you should read that document although much of it is Plan 9-specific.  The current document provides a summary of the syntax and the differences with  what is explained in that document, and  describes the peculiarities that apply when writing assembly code to interact with Go…
- `[]`
  > However, when referring to a function argument this way, it is necessary to place a name  at the beginning, as in first_arg+0(FP) and second_arg+8(FP).  (The meaning of the offset—offset from the frame pointer—distinct  from its use with SB, where it is an offset from the symbol.)  The assembler enforces this convention, rejecting plain 0(FP) and 8(FP).  The actual name is semantically irrelevant but should be used to document  the argument's name.  It is worth stressing that FP is always a  pseudo-register, not a hardware  register, even on architectures with a hardware frame pointer.  For as…
- `[]`
  > and declares runtime·tlsoffset, a 4-byte, implicitly zeroed variable that  contains no pointers.  There may be one or two arguments to the directives.  If there are two, the first is a bit mask of flags,  which can be written as numeric expressions, added or or-ed together,  or can be set symbolically for easier absorption by a human.  Their values, defined in the standard #include file textflag.h, are:  NOPROF = 1  (For TEXT items.)  Don't profile the marked function. This flag is deprecated.  DUPOK = 2  It is legal to have multiple instances of this symbol in a single binary.  The linker wil…
- `['include file funcdata.h.']`
  > include file funcdata.h.  If a function has no arguments and no results,  the pointer information can be omitted.  This is indicated by an argument size annotation of $n-0  on the TEXT instruction.  Otherwise, pointer information must be provided by  a Go prototype for the function in a Go source file,  even for assembly functions not called directly from Go.  (The prototype will also let go vet check the argument references.)  At the start of the function, the arguments are assumed  to be initialized but the results are assumed uninitialized.  If the results will hold live pointers during a c…
- `['include "go_tls.h"']`
  > include "go_tls.h"
- `['include "go_asm.h"']`
  > include "go_asm.h"  ...  get_tls(CX)  MOVL	g(CX), AX // Move g into AX.  MOVL	g_m(AX), BX // Move g.m into BX.  The get_tls macro is also defined on amd64.  Addressing modes:  (DI)(BX*2): The location at address DI plus BX*2.  64(DI)(BX*2): The location at address DI plus BX*2 plus 64.  These modes accept only 1, 2, 4, and 8 as scale factors.  When using the compiler and assembler's  -dynlink or -shared modes,  any load or store of a fixed memory location such as a global variable  must be assumed to overwrite CX.  Therefore, to be safe for use with these modes,  assembly sources should typica…

`verdict_relevant:` ______   `notes:` ______

---

## 136. `92f06edf6265faeb`  (kubernetes)

**Query** (zh / concept): Deployment 控制器是怎么保证 Pod 达到期望副本数的？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/workloads/controllers/deployment.md`

**Claimed section** (unverified): `['Deployments']`

**Actual sections in the index:**

- `[]`
  > A _Deployment_ provides declarative updates for {{< glossary_tooltip text="Pods" term_id="pod" >}} and  {{< glossary_tooltip term_id="replica-set" text="ReplicaSets" >}}.  You describe a _desired state_ in a Deployment, and the Deployment {{< glossary_tooltip term_id="controller" >}} changes the actual state to the desired state at a controlled rate. You can define Deployments to create new ReplicaSets, or to remove existing Deployments and adopt all their resources with new Deployments.  {{< note >}}  Do not manage ReplicaSets owned by a Deployment. Consider opening an issue in the main Kuber…
- `['Use Case']`
  > Use Case  The following are typical use cases for Deployments:  * [Create a Deployment to rollout a ReplicaSet](#creating-a-deployment). The ReplicaSet creates Pods in the background. Check the status of the rollout to see if it succeeds or not.  * [Declare the new state of the Pods](#updating-a-deployment) by updating the PodTemplateSpec of the Deployment. A new ReplicaSet is created, and the Deployment gradually scales it up while scaling down the old ReplicaSet, ensuring Pods are replaced at a controlled rate. Each new ReplicaSet updates the revision of the Deployment.  * [Rollback to an ea…
- `['Use Case', 'Creating a Deployment']`
  > Creating a Deployment  The following is an example of a Deployment. It creates a ReplicaSet to bring up three `nginx` Pods:  {{% code_sample file="controllers/nginx-deployment.yaml" %}}  In this example:  * A Deployment named `nginx-deployment` is created, indicated by the  `.metadata.name` field. This name will become the basis for the ReplicaSets  and Pods which are created later. See [Writing a Deployment Spec](#writing-a-deployment-spec)  for more details.  * The Deployment creates a ReplicaSet that creates three replicated Pods, indicated by the `.spec.replicas` field.  * The `.spec.selec…
- `['Use Case', 'Creating a Deployment', 'Pod-template-hash label']`
  > Pod-template-hash label  {{< caution >}}  Do not change this label.  {{< /caution >}}  The `pod-template-hash` label is added by the Deployment controller to every ReplicaSet that a Deployment creates or adopts.  This label ensures that child ReplicaSets of a Deployment do not overlap. It is generated by hashing the `PodTemplate` of the ReplicaSet and using the resulting hash as the label value that is added to the ReplicaSet selector, Pod template labels,  and in any existing Pods that the ReplicaSet might have.
- `['Use Case', 'Updating a Deployment']`
  > Updating a Deployment  {{< note >}}  A Deployment's rollout is triggered if and only if the Deployment's Pod template (that is, `.spec.template`)  is changed, for example if the labels or container images of the template are updated. Other updates, such as scaling the Deployment, do not trigger a rollout.  {{< /note >}}  Follow the steps given below to update your Deployment:  1. Let's update the nginx Pods to use the `nginx:1.16.1` image instead of the `nginx:1.14.2` image.  kubectl set image deployment.v1.apps/nginx-deployment nginx=nginx:1.16.1  or use the following command:  kubectl set im…
- `['Use Case', 'Updating a Deployment', 'Rollover (aka multiple updates in-flight)']`
  > Rollover (aka multiple updates in-flight)  Each time a new Deployment is observed by the Deployment controller, a ReplicaSet is created to bring up  the desired Pods. If the Deployment is updated, the existing ReplicaSet that controls Pods whose labels  match `.spec.selector` but whose template does not match `.spec.template` is scaled down. Eventually, the new  ReplicaSet is scaled to `.spec.replicas` and all old ReplicaSets is scaled to 0.  If you update a Deployment while an existing rollout is in progress, the Deployment creates a new ReplicaSet  as per the update and start scaling that up…

`verdict_relevant:` ______   `notes:` ______

---

## 137. `0b2579b17cf80050`  (python)

**Query** (zh / code_api): pathlib 的 Path 对象有哪些常用方法用来读写文件和遍历目录？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/pathlib.rst`

**Claimed section** (unverified): `['Basic use']`

**Actual sections in the index:**

- `[]`
  > :mod:`!pathlib` --- Object-oriented filesystem paths  ====================================================  .. module:: pathlib  :synopsis: Object-oriented filesystem paths  .. versionadded:: 3.4  **Source code:** :source:`Lib/pathlib/`  .. index:: single: path; operations  --------------  This module offers classes representing filesystem paths with semantics  appropriate for different operating systems. Path classes are divided  between :ref:`pure paths <pure-paths>`, which provide purely computational  operations without I/O, and :ref:`concrete paths <concrete-paths>`, which  inherit from p…
- `['. If you want to manipulate Windows paths on a Unix machine (or vice versa).']`
  > . If you want to manipulate Windows paths on a Unix machine (or vice versa).  You cannot instantiate a :class:`WindowsPath` when running on Unix, but you  can instantiate :class:`PureWindowsPath`.
- `['. You want to make sure that your code only manipulates paths without actually']`
  > . You want to make sure that your code only manipulates paths without actually  accessing the OS. In this case, instantiating one of the pure classes may be  useful since those simply don't have any OS-accessing operations.  .. seealso::  :pep:`428`: The pathlib module -- object-oriented filesystem paths.  .. seealso::  For low-level path manipulation on strings, you can also use the  :mod:`os.path` module.  Basic use  ---------  Importing the main class::  >>> from pathlib import Path  Listing subdirectories::  >>> p = Path('.')  >>> [x for x in p.iterdir() if x.is_dir()]  [PosixPath('.hg'),…
- `['. You want to make sure that your code only manipulates paths without actually']`
  > *"A pathname that begins with two successive slashes may be interpreted in  an implementation-defined manner, although more than two leading slashes  shall be treated as a single slash."*  .. attribute:: PurePath.anchor  The concatenation of the drive and root::  >>> PureWindowsPath('c:/Program Files/').anchor  'c:\\'  >>> PureWindowsPath('c:Program Files/').anchor  'c:'  >>> PurePosixPath('/etc').anchor  '/'  >>> PureWindowsPath('//host/share').anchor  '\\\\host\\share\\'  .. attribute:: PurePath.parents  An immutable sequence providing access to the logical ancestors of  the path::  >>> p =…
- `['. You want to make sure that your code only manipulates paths without actually']`
  > >>> p = PureWindowsPath('c:/')  >>> p.with_stem('')  Traceback (most recent call last):  File "<stdin>", line 1, in <module>  File "/home/antoine/cpython/default/Lib/pathlib.py", line 861, in with_stem  return self.with_name(stem + self.suffix)  File "/home/antoine/cpython/default/Lib/pathlib.py", line 851, in with_name  raise ValueError("%r has an empty name" % (self,))  ValueError: PureWindowsPath('c:/') has an empty name  .. versionadded:: 3.9  .. method:: PurePath.with_suffix(suffix)  Return a new path with the :attr:`suffix` changed. If the original path  doesn't have a suffix, the new *s…
- `['. You want to make sure that your code only manipulates paths without actually']`
  > :meth:`~Path.is_block_device`, :meth:`~Path.is_char_device`,  :meth:`~Path.is_fifo`, :meth:`~Path.is_socket` now return ``False``  instead of raising an exception for paths that contain characters  unrepresentable at the OS level.  .. versionchanged:: 3.14  The methods given above now return ``False`` instead of raising any  :exc:`OSError` exception from the operating system. In previous versions,  some kinds of :exc:`OSError` exception are raised, and others suppressed.  The new behaviour is consistent with :func:`os.path.exists`,  :func:`os.path.isdir`, etc. Use :meth:`~Path.stat` to retriev…

`verdict_relevant:` ______   `notes:` ______

---

## 138. `3caf8f90fac78b2e`  (go)

**Query** (zh / troubleshooting): go vet 新增的 scannererr 分析器是用来检查什么问题的？

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/next/3-tools.md`

**Claimed section** (unverified): `['Tools', 'Vet']`

**Actual sections in the index:**

- `['Tools {#tools}']`
  > Tools {#tools}
- `['Tools {#tools}', 'Go command {#go-command}']`
  > Go command {#go-command}
- `['Tools {#tools}', 'Go command {#go-command}', 'Cgo {#cgo}']`
  > Cgo {#cgo}
- `['Tools {#tools}', 'Go command {#go-command}', 'Vet {#vet}']`
  > Vet {#vet}  The new [`scannererr`](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/scannererr)  analyzer checks for failure to handle scanner errors after a loop  around [bufio.Scanner.Scan], which may cause scanning or I/O errors to  go unreported.  The [`sqlrowserr`](https://pkg.go.dev/golang.org/x/tools/go/analysis/passes/sqlrowserr)  analyzer performs a similar check for loops around [sql.Rows.Next],  so that iteration errors are correctly distinguished from a smaller result.

`verdict_relevant:` ______   `notes:` ______

---

## 139. `9e1c890941874c21`  (git)

**Query** (en / concept): What is the difference between rebase and merge in terms of rewriting history?

**Document**: `git@a97fcc37c2bc6340a8d7ce78dedf227aac4e9aa7:Documentation/git-blame.adoc`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > git-blame(1)  ============  NAME  ----  git-blame - Show what revision and author last modified each line of a file  SYNOPSIS  --------  [synopsis]  git blame [-c] [-b] [-l] [--root] [-t] [-f] [-n] [-s] [-e] [-p] [-w] [--incremental]  [-L <range>] [-S <revs-file>] [-M] [-C] [-C] [-C] [--since=<date>]  [--ignore-rev <rev>] [--ignore-revs-file <file>]  [--color-lines] [--color-by-age] [--progress] [--abbrev=<n>]  [ --contents <file> ] [<rev> | --reverse <rev>..<rev>] [--] <file>  DESCRIPTION  -----------  Annotates each line in the given file with information from the revision which  last modifi…
- `['count the number of lines attributed to each author']`
  > count the number of lines attributed to each author  git blame --line-porcelain file |  sed -n 's/^author //p' |  sort | uniq -c | sort -rn  SPECIFYING RANGES  -----------------  Unlike `git blame` and `git annotate` in older versions of git, the extent  of the annotation can be limited to both line ranges and revision  ranges. The `-L` option, which limits annotation to a range of lines, may be  specified multiple times.  When you are interested in finding the origin for  lines 40-60 for file `foo`, you can use the `-L` option like so  (they mean the same thing -- both ask for 21 lines starti…

`verdict_relevant:` ______   `notes:` ______

---

## 140. `d818248ed3c1aa61`  (postgresql)

**Query** (en / code_api): How does PostgreSQL decide how to convert types in expressions and comparisons?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/typeconv.sgml`

**Claimed section** (unverified): `['Type Conversion']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/typeconv.sgml -->  <chapter id="typeconv">  <title>Type Conversion</title>  <indexterm zone="typeconv">  <primary>data type</primary>  <secondary>conversion</secondary>  </indexterm>  <para>  <acronym>SQL</acronym> statements can, intentionally or not, require  the mixing of different data types in the same expression.  <productname>PostgreSQL</productname> has extensive facilities for  evaluating mixed-type expressions.  </para>  <para>  In many cases a user does not need  to understand the details of the type conversion mechanism.  However, implicit conversions done by <pro…
- `[]`
  > visible in the current search path (see <xref linkend="ddl-schemas-path"/>).  If a qualified operator name was given, only operators in the specified  schema are considered.  </para>  <substeps>  <step performance="optional">  <para>  If the search path finds multiple operators with identical argument types,  only the one appearing earliest in the path is considered. Operators with  different argument types are considered on an equal footing regardless of  search path position.  </para>  </step>  </substeps>  </step>  <step id="op-resol-exact-match" performance="required">  <para>  Check for a…
- `[]`
  > absolute-value operations for various numeric data types. One of these  entries is for type <type>float8</type>, which is the preferred type in  the numeric category. Therefore, <productname>PostgreSQL</productname>  will use that entry when faced with an <type>unknown</type> input:  <screen>  SELECT @ '-4.5' AS "abs";  abs  -----  4.5  (1 row)  </screen>  Here the system has implicitly resolved the unknown-type literal as type  <type>float8</type> before applying the chosen operator. We can verify that  <type>float8</type> and not some other type was used:  <screen>  SELECT @ '-4.5e500' AS "a…
- `[]`
  > Functions that have default values for parameters are considered to match any  call that omits zero or more of the defaultable parameter positions. If more  than one such function matches a call, the one appearing earliest in the  search path is used. If there are two or more such functions in the same  schema with identical parameter types in the non-defaulted positions (which is  possible if they have different sets of defaultable parameters), the system  will not be able to determine which to prefer, and so an <quote>ambiguous  function call</quote> error will result if no better match to t…
- `[]`
  > 1 | 1 | 1  (1 row)  </screen>  However, the first and second calls will prefer more-specific functions, if  available:  <screen>  CREATE FUNCTION public.variadic_example(numeric) RETURNS int  LANGUAGE sql AS 'SELECT 2';  CREATE FUNCTION  CREATE FUNCTION public.variadic_example(int) RETURNS int  LANGUAGE sql AS 'SELECT 3';  CREATE FUNCTION  SELECT public.variadic_example(0),  public.variadic_example(0.0),  public.variadic_example(VARIADIC array[0.0]);  variadic_example | variadic_example | variadic_example  ------------------+------------------+------------------  3 | 2 | 1  (1 row)  </screen>…
- `[]`
  > </footnote>  </para>  </step>  <step performance="required">  <para>  If all inputs are of type <type>unknown</type>, resolve as type  <type>text</type> (the preferred type of the string category).  Otherwise, <type>unknown</type> inputs are ignored for the purposes  of the remaining rules.  </para>  </step>  <step performance="required">  <para>  If the non-unknown inputs are not all of the same type category, fail.  </para>  </step>  <step performance="required">  <para>  Select the first non-unknown input type as the candidate type,  then consider each other non-unknown input type, left to…

`verdict_relevant:` ______   `notes:` ______

---

## 141. `c2c25103df5d5928`  (python)

**Query** (zh / concept): Python 多重继承时方法解析顺序（MRO）是怎么确定的？

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/howto/mro.rst`

**Claimed section** (unverified): `['The Python 2.3 Method Resolution Order', 'The C3 Method Resolution Order']`

**Actual sections in the index:**

- `[]`
  > .. _python_2.3_mro:  The Python 2.3 Method Resolution Order  ======================================  .. note::  This is a historical document, provided as an appendix to the official  documentation.  The Method Resolution Order discussed here was *introduced* in Python 2.3,  but it is still used in later versions -- including Python 3.  By `Michele Simionato <https://github.com/micheles>`__.  :Abstract:  *This document is intended for Python programmers who want to  understand the C3 Method Resolution Order used in Python 2.3.  Although it is not intended for newbies, it is quite pedagogical w…
- `[]`
  > The *head* of the list is its first element::  head = C1  whereas the *tail* is the rest of the list::  tail = C2 ... CN.  I shall also use the notation::  C + (C1 C2 ... CN) = C C1 C2 ... CN  to denote the sum of the lists [C] + [C1, C2, ... ,CN].  Now I can explain how the MRO works in Python 2.3.  Consider a class C in a multiple inheritance hierarchy, with C  inheriting from the base classes B1, B2, ... , BN. We want to  compute the linearization L[C] of the class C. The rule is the  following:  *the linearization of C is the sum of C plus the merge of the  linearizations of the parents an…
- `[]`
  > >>> A.mro() # doctest: +NORMALIZE_WHITESPACE  [<class 'A'>, <class 'B'>, <class 'E'>,  <class 'C'>, <class 'D'>, <class 'F'>,  <class 'object'>]  Finally, let me consider the example discussed in the first section,  involving a serious order disagreement. In this case, it is  straightforward to compute the linearizations of O, X, Y, A and B:  .. code-block:: text  L[O] = 0  L[X] = X O  L[Y] = Y O  L[A] = A X Y O  L[B] = B Y X O  However, it is impossible to compute the linearization for a class C  that inherits from A and B::  L[C] = C + merge(AXYO, BYXO, AB)  = C + A + merge(XYO, BYXO, B)  =…
- `[]`
  > L[E] = E O  L[K1]= K1 A B C O  L[K2]= K2 D B E O  L[K3]= K3 D A O  L[Z] = Z K1 K2 K3 D A B C E O  Python 2.2 gives exactly the same linearizations for A, B, C, D, E, K1,  K2 and K3, but a different linearization for Z::  L[Z,P22] = Z K1 K3 A K2 D B C E O  It is clear that this linearization is *wrong*, since A comes before D  whereas in the linearization of K3 A comes *after* D. In other words, in  K3 methods derived by D override methods derived by A, but in Z, which  still is a subclass of K3, methods derived by A override methods derived  by D! This is a violation of monotonicity. Moreover,…
- `['<mro.py>']`
  > <mro.py>  """C3 algorithm by Samuele Pedroni (with readability enhanced by me)."""  class __metaclass__(type):  "All classes are metamagically modified to be nicely printed"  __repr__ = lambda cls: cls.__name__  class ex_2:  "Serious order disagreement" #From Guido  class O: pass  class X(O): pass  class Y(O): pass  class A(X,Y): pass  class B(Y,X): pass  try:  class Z(A,B): pass #creates Z(A,B) in Python 2.2  except TypeError:  pass # Z(A,B) cannot be created in Python 2.3  class ex_5:  "My first example"  class O: pass  class F(O): pass  class E(O): pass  class D(O): pass  class C(D,F): pass…
- `['</mro.py>']`
  > </mro.py>  That's all folks,  enjoy !  Resources  ---------  .. [#] The thread on python-dev started by Samuele Pedroni:  https://mail.python.org/pipermail/python-dev/2002-October/029035.html  .. [#] The paper *A Monotonic Superclass Linearization for Dylan*:  https://doi.org/10.1145/236337.236343  .. [#] Guido van Rossum's essay, *Unifying types and classes in Python 2.2*:  https://web.archive.org/web/20140210194412/http://www.python.org/download/releases/2.2.2/descrintro

`verdict_relevant:` ______   `notes:` ______

---

## 142. `e16d7bcdcabcb08f`  (kubernetes)

**Query** (en / concept): What is the lifecycle of a pod in Kubernetes, from creation to termination?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/workloads/pods/pod-lifecycle.md`

**Claimed section** (unverified): `['Pod Lifecycle']`

**Actual sections in the index:**

- `[]`
  > This page describes the lifecycle of a Pod. Pods follow a defined lifecycle, starting  in the `Pending` [phase](#pod-phase), moving through `Running` if at least one  of its primary containers starts OK, and then through either the `Succeeded` or  `Failed` phases depending on whether any container in the Pod terminated in failure.  While a Pod runs, the kubelet manages containers and translates the Pod's spec  for the container runtime. The kubelet also manages executing  [probes](#container-probes) that track the health of your application.  Like individual application containers, Pods are co…
- `['Pod lifetime']`
  > Pod lifetime  While a Pod is running, the kubelet is able to restart containers to handle  some kind of faults. Within a Pod, Kubernetes tracks different container  [states](#container-states) and determines what action to take to make the Pod  healthy again. This is done in a [polling  loop](/docs/reference/node/kubelet-sync-loop/) that periodically reconciles the  desired state (a Pod spec) with the actual state of the running containers.  In the Kubernetes API, Pods have both a specification and an actual status. The  status for a Pod object consists of a set of [Pod conditions](#pod-condit…
- `['Pod lifetime', 'Pods and fault recovery {#pod-fault-recovery}']`
  > Pods and fault recovery {#pod-fault-recovery}  If one of the containers in the Pod fails, then Kubernetes may try to restart that  specific container.  Read [How Pods handle problems with containers](#container-restarts) to learn more.  Pods can however fail in a way that the cluster cannot recover from, and in that case  Kubernetes does not attempt to heal the Pod further; instead, Kubernetes deletes the  Pod and relies on other components to provide automatic healing.  If a Pod is scheduled to a {{< glossary_tooltip text="node" term_id="node" >}} and that  node then fails, the Pod is treated…
- `['Pod lifetime', 'Pods and fault recovery {#pod-fault-recovery}', 'Associated lifetimes']`
  > Associated lifetimes  When something is said to have the same lifetime as a Pod, such as a  {{< glossary_tooltip term_id="volume" text="volume" >}},  that means that the thing exists as long as that specific Pod (with that exact UID)  exists. If that Pod is deleted for any reason, and even if an identical replacement  is created, the related thing (a volume, in this example) is also destroyed and  created anew.  {{< figure src="/images/docs/pod.svg" title="Figure 1." class="diagram-medium" caption="A multi-container Pod that contains a file puller [sidecar](/docs/concepts/workloads/pods/sideca…
- `['Pod lifetime', 'Pod phase']`
  > Pod phase  A Pod's `status` field is a  [PodStatus](/docs/reference/generated/kubernetes-api/{{< param "version" >}}/#podstatus-v1-core)  object, which has a `phase` field.  The phase of a Pod is a simple, high-level summary of where the Pod is in its  lifecycle. The phase is not intended to be a comprehensive rollup of observations  of container or Pod state, nor is it intended to be a comprehensive state machine.  The number and meanings of Pod phase values are tightly guarded.  Other than what is documented here, nothing should be assumed about Pods that  have a given `phase` value.  Here a…
- `['Pod lifetime', 'Container states']`
  > Container states  As well as the [phase](#pod-phase) of the Pod overall, Kubernetes tracks the state of  each container inside a Pod. You can use  [container lifecycle hooks](/docs/concepts/containers/container-lifecycle-hooks/) to  trigger events to run at certain points in a container's lifecycle.  Once the {{< glossary_tooltip text="scheduler" term_id="kube-scheduler" >}}  assigns a Pod to a Node, the kubelet starts creating containers for that Pod  using a {{< glossary_tooltip text="container runtime" term_id="container-runtime" >}}.  There are three possible container states: `Waiting`, `…

`verdict_relevant:` ______   `notes:` ______

---

## 143. `088cb0886d7ef18c`  (docker)

**Query** (zh / config): Compose 里环境变量的优先级是怎么定的？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/how-tos/environment-variables/envvars-precedence.md`

**Claimed section** (unverified): `['Environment variables precedence in Docker Compose']`

**Actual sections in the index:**

- `[]`
  > When the same environment variable is set in multiple sources, Docker Compose follows a precedence rule to determine the value for that variable in your container's environment.  This page explains how Docker Compose determines the final value of an environment variable when it's defined in multiple locations.  The order of precedence (highest to lowest) is as follows:  1. Set using [`docker compose run -e` in the CLI](set-environment-variables.md#set-environment-variables-with-docker-compose-run---env).  2. Set with either the `environment` or `env_file` attribute but with the value interpola…
- `['Simple example']`
  > Simple example  In the following example, a different value for the same environment variable in an `.env` file and with the `environment` attribute in the Compose file:  $ cat ./webapp.env  NODE_ENV=test  $ cat compose.yaml  services:  webapp:  image: 'webapp'  env_file:  - ./webapp.env  environment:  - NODE_ENV=production  The environment variable defined with the `environment` attribute takes precedence.  $ docker compose run webapp env | grep NODE_ENV  NODE_ENV=production
- `['Simple example', 'Advanced example']`
  > Advanced example  The following table uses `VALUE`, an environment variable defining the version for an image, as an example.
- `['Simple example', 'Advanced example', 'How the table works']`
  > How the table works  Each column represents a context from where you can set a value, or substitute in a value for `VALUE`.  The columns `Host OS environment` and `.env` file is listed only for illustration purposes. In reality, they don't result in a variable in the container by itself, but in conjunction with either the `environment` or `env_file` attribute.  Each row represents a combination of contexts where `VALUE` is set, substituted, or both. The **Result** column indicates the final value for `VALUE` in each scenario.  |  # |  `docker compose run`  |  `environment` attribute  |  `env_f…
- `['Simple example', 'Advanced example', 'Understanding precedence results']`
  > Understanding precedence results  Result 1: The local environment takes precedence, but the Compose file is not set to replicate this inside the container, so no such variable is set.  Result 2: The `env_file` attribute in the Compose file defines an explicit value for `VALUE` so the container environment is set accordingly.  Result 3: The `environment` attribute in the Compose file defines an explicit value for `VALUE`, so the container environment is set accordingly.  Result 4: The image's `ENV` directive declares the variable `VALUE`, and since the Compose file is not set to override this v…
- `['Simple example', 'Next steps']`
  > Next steps  - [Set environment variables in Compose](set-environment-variables.md)  - [Use variable interpolation in Compose files](variable-interpolation.md)

`verdict_relevant:` ______   `notes:` ______

---

## 144. `5d4607af8df7290e`  (go)

**Query** (en / troubleshooting): A library's behavior changed after a Go upgrade; how can GODEBUG settings temporarily restore the previous behavior?

**Document**: `go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/godebug.md`

**Claimed section** (unverified): `['Go, Backwards Compatibility, and GODEBUG', 'GODEBUG History']`

**Actual sections in the index:**

- `['Introduction {#intro}']`
  > Introduction {#intro}  Go's emphasis on backwards compatibility is one of its key strengths.  There are, however, times when we cannot maintain complete compatibility.  If code depends on buggy (including insecure) behavior,  then fixing the bug will break that code.  New features can also have similar impacts:  enabling the HTTP/2 use by the HTTP client broke programs  connecting to servers with buggy HTTP/2 implementations.  These kinds of changes are unavoidable and  [permitted by the Go 1 compatibility rules](/doc/go1compat).  Even so, Go provides a mechanism called GODEBUG to  reduce the…
- `['Introduction {#intro}', 'Default GODEBUG Values {#default}']`
  > Default GODEBUG Values {#default}  When a GODEBUG setting is not listed in the environment variable,  its value is derived from three sources:  the defaults for the Go toolchain used to build the program,  amended to match the Go version listed in `go.mod`,  and then overridden by explicit `//go:debug` lines in the program.  The [GODEBUG History](#history) gives the exact defaults for each Go toolchain version.  For example, Go 1.21 introduces the `panicnil` setting,  controlling whether `panic(nil)` is allowed;  it defaults to `panicnil=0`, making `panic(nil)` a run-time error.  Using `panicn…
- `['Introduction {#intro}', 'GODEBUG History {#history}']`
  > GODEBUG History {#history}  This section documents the GODEBUG settings introduced and removed in each major Go release  for compatibility reasons.  Packages or programs may define additional settings for internal debugging purposes;  for example,  see the [runtime documentation](/pkg/runtime#hdr-Environment_Variables)  and the [go command documentation](/cmd/go#hdr-Build_and_test_caching).
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.27']`
  > Go 1.27  Go 1.27 removed the `gotypesalias` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsunsafeekm` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tlsrsakex` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `tls3des` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `tls10server` setting, as noted in the [Go 1.22](#go-122) section.  Go 1.27 removed the `x509keypairleaf` setting, as noted in the [Go 1.23](#go-123) section.  Go 1.27 removed the `asynctimerchan` setting, as noted in the […
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.26']`
  > Go 1.26  Go 1.26 added a new `httpcookiemaxnum` setting that controls the maximum number  of cookies that net/http will accept when parsing HTTP headers. If the number of  cookie in a header exceeds the number set in `httpcookiemaxnum`, cookie parsing  will fail early. The default value is `httpcookiemaxnum=3000`. Setting  `httpcookiemaxnum=0` will allow the cookie parsing to accept an indefinite  number of cookies. To avoid denial of service attacks, this setting and default  was backported to Go 1.25.2 and Go 1.24.8.  Go 1.26 added a new `urlmaxqueryparams` setting that controls the maximum…
- `['Introduction {#intro}', 'GODEBUG History {#history}', 'Go 1.25']`
  > Go 1.25  Go 1.25 added a new `decoratemappings` setting that controls whether the Go  runtime annotates OS anonymous memory mappings with context about their  purpose. These annotations appear in /proc/self/maps and /proc/self/smaps as  "[anon: Go: ...]". This setting is only used on Linux. For Go 1.25, it defaults  to `decoratemappings=1`, enabling annotations. Using `decoratemappings=0`  reverts to the pre-Go 1.25 behavior. This setting is fixed at program startup  time, and can't be modified by changing the `GODEBUG` environment variable  after the program starts.  Go 1.25 added a new `embe…

`verdict_relevant:` ______   `notes:` ______

---

## 145. `423151c4ec7aef0b`  (kubernetes)

**Query** (zh / code_api): 容器生命周期钩子 postStart 和 preStop 怎么用？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/containers/container-lifecycle-hooks.md`

**Claimed section** (unverified): `['Container Lifecycle Hooks']`

**Actual sections in the index:**

- `[]`
  > This page describes how kubelet managed Containers can use the Container lifecycle hook framework  to run code triggered by events during their management lifecycle.
- `['Overview']`
  > Overview  Analogous to many programming language frameworks that have component lifecycle hooks, such as Angular,  Kubernetes provides Containers with lifecycle hooks.  The hooks enable Containers to be aware of events in their management lifecycle  and run code implemented in a handler when the corresponding lifecycle hook is executed.
- `['Overview', 'Container hooks']`
  > Container hooks  There are two hooks that are exposed to Containers:  `PostStart`  This hook is executed immediately after a container is created.  It runs **concurrently** with the container's `ENTRYPOINT` (main process),  meaning the hook may run before, during, or after the main process starts.  No parameters are passed to the handler.  {{< note >}}  While the hook runs concurrently with the container process,  it can delay container status updates;  the container may not transition to `Running` until the hook completes.  {{< /note >}}  `PreStop`  This hook is called immediately before a co…
- `['Overview', 'Container hooks', 'Hook handler implementations']`
  > Hook handler implementations  Containers can access a hook by implementing and registering a handler for that hook.  There are three types of hook handlers that can be implemented for Containers:  * Exec - Executes a specific command, such as `pre-stop.sh`, inside the cgroups and namespaces of the Container.  Resources consumed by the command are counted against the Container.  * HTTP - Executes an HTTP request against a specific endpoint on the Container.  * Sleep - Pauses the container for a specified duration.
- `['Overview', 'Container hooks', 'Hook handler execution']`
  > Hook handler execution  When a Container lifecycle management hook is called,  the Kubernetes management system executes the handler according to the hook action,  `httpGet`, `tcpSocket` ([deprecated](/docs/reference/generated/kubernetes-api/v1.35/#lifecyclehandler-v1-core))  and `sleep` are executed by the kubelet process, and `exec` is executed in the container.  The `PostStart` hook handler call is initiated when a container is created,  meaning the container ENTRYPOINT and the `PostStart` hook are triggered simultaneously.  (This means it generally doesn't make sense to use an HTTP hook fo…
- `['Overview', 'Container hooks', 'Hook delivery guarantees']`
  > Hook delivery guarantees  Hook delivery is intended to be *at least once*,  which means that a hook may be called multiple times for any given event,  such as for `PostStart` or `PreStop`.  It is up to the hook implementation to handle this correctly.  Generally, only single deliveries are made.  If, for example, an HTTP hook receiver is down and is unable to take traffic,  there is no attempt to resend.  In some rare cases, however, double delivery may occur.  For instance, if a kubelet restarts in the middle of sending a hook,  the hook might be resent after the kubelet comes back up.

`verdict_relevant:` ______   `notes:` ______

---

## 146. `69c0e9dd444b72ff`  (docker)

**Query** (zh / concept): Docker Swarm 的节点之间是怎么做加密通信（PKI）的？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/network/drivers/host.md`

**Claimed section** (unverified): `[]`

**Actual sections in the index:**

- `[]`
  > If you use the `host` network mode for a container, that container's network  stack isn't isolated from the Docker host (the container shares the host's  networking namespace), and the container doesn't get its own IP-address allocated.  For instance, if you run a container which binds to port 80 and you use `host`  networking, the container's application is available on port 80 on the host's IP  address.  > [!NOTE]  >  > Given that the container does not have its own IP-address when using  > `host` mode networking, [port-mapping](overlay.md#publish-ports) doesn't  > take effect, and the `-p`,…
- `['Platform support']`
  > Platform support  The host networking driver is supported on:  - Docker Engine on Linux  - Docker Desktop version 4.34 and later (requires enabling the feature in  Settings)  > [!NOTE]  > For Docker Desktop users, see the [Docker Desktop section](#docker-desktop)  > below for setup instructions.  You can also use a `host` network for a swarm service, by passing `--network host`  to the `docker service create` command. In this case, control traffic (traffic  related to managing the swarm and the service) is still sent across an overlay  network, but the individual swarm service containers send…
- `['Platform support', 'Docker Desktop']`
  > Docker Desktop  Host networking is supported on Docker Desktop version 4.34 and later.  To enable this feature:  1. Sign in to your Docker account in Docker Desktop.  2. Navigate to **Settings**.  3. Under the **Resources** tab, select **Network**.  4. Check the **Enable host networking** option.  5. Select **Apply and restart**.  This feature works in both directions. This means you can  access a server that is running in a container from your host and you can access  servers running on your host from any container that is started with host  networking enabled. TCP as well as UDP are supporte…
- `['Platform support', 'Docker Desktop', 'Examples']`
  > Examples  The following command starts netcat in a container that listens on port `8000`:  $ docker run --rm -it --net=host nicolaka/netshoot nc -lkv 0.0.0.0 8000  Port `8000` will then be available on the host and you can connect to it with the following  command from another terminal:  $ nc localhost 8000  What you type in here will then appear on the terminal where the container is  running.  To access a service running on the host from the container, you can start a container with  host networking enabled with this command:  $ docker run --rm -it --net=host nicolaka/netshoot  If you then w…
- `['Platform support', 'Docker Desktop', 'Limitations']`
  > Limitations  - Processes inside the container cannot bind to the IP addresses of the host  because the container has no direct access to the interfaces of the host.  - The host network feature of Docker Desktop works on layer 4. This means that  unlike with Docker on Linux, network protocols that operate below TCP or UDP are  not supported.  - This feature doesn't work with Enhanced Container Isolation enabled, since  isolating your containers from the host and allowing them access to the host  network contradict each other.  - Only Linux containers are supported. Host networking does not work…
- `['Platform support', 'Usage example']`
  > Usage example  This example shows how to start an Nginx container that binds directly to port  80 on the Docker host. From a networking perspective, this provides the same  level of isolation as if Nginx were running directly on the host, but the  container remains isolated in all other aspects (storage, process namespace,  user namespace).

`verdict_relevant:` ______   `notes:` ______

---

## 147. `39876386a203b121`  (postgresql)

**Query** (en / concept): What data types does PostgreSQL provide for numeric, character, and date/time values?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/datatype.sgml`

**Claimed section** (unverified): `['Data Types']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/datatype.sgml -->  <chapter id="datatype">  <title>Data Types</title>  <indexterm zone="datatype">  <primary>data type</primary>  </indexterm>  <indexterm>  <primary>type</primary>  <see>data type</see>  </indexterm>  <para>  <productname>PostgreSQL</productname> has a rich set of native data  types available to users. Users can add new types to  <productname>PostgreSQL</productname> using the <xref  linkend="sql-createtype"/> command.  </para>  <para>  <xref linkend="datatype-table"/> shows all the built-in general-purpose data  types. Most of the alternative names listed in…
- `[]`
  > space is at a premium. The <type>bigint</type> type is designed to be  used when the range of the <type>integer</type> type is insufficient.  </para>  <para>  <acronym>SQL</acronym> only specifies the integer types  <type>integer</type> (or <type>int</type>),  <type>smallint</type>, and <type>bigint</type>. The  type names <type>int2</type>, <type>int4</type>, and  <type>int8</type> are extensions, which are also used by some  other <acronym>SQL</acronym> database systems.  </para>  </sect2>  <sect2 id="datatype-numeric-decimal">  <title>Arbitrary Precision Numbers</title>  <indexterm>  <prima…
- `[]`
  > value (including <literal>NaN</literal>). In order to allow  <type>numeric</type> values to be sorted and used in tree-based  indexes, <productname>PostgreSQL</productname> treats <literal>NaN</literal>  values as equal, and greater than all non-<literal>NaN</literal>  values.  </para>  </note>  <para>  The types <type>decimal</type> and <type>numeric</type> are  equivalent. Both types are part of the <acronym>SQL</acronym>  standard.  </para>  <para>  When rounding values, the <type>numeric</type> type rounds ties away  from zero, while (on most machines) the <type>real</type>  and <type>doub…
- `[]`
  > </programlisting>  is equivalent to specifying:  <programlisting>  CREATE SEQUENCE <replaceable class="parameter">tablename</replaceable>_<replaceable class="parameter">colname</replaceable>_seq AS integer;  CREATE TABLE <replaceable class="parameter">tablename</replaceable> (  <replaceable class="parameter">colname</replaceable> integer NOT NULL DEFAULT nextval('<replaceable class="parameter">tablename</replaceable>_<replaceable class="parameter">colname</replaceable>_seq')  );  ALTER SEQUENCE <replaceable class="parameter">tablename</replaceable>_<replaceable class="parameter">colname</repla…
- `[]`
  > <type>text</type> is <productname>PostgreSQL</productname>'s native  string data type, in that most built-in functions operating on strings  are declared to take or return <type>text</type> not <type>character  varying</type>. For many purposes, <type>character varying</type>  acts as though it were a <link linkend="domains">domain</link>  over <type>text</type>.  </para>  <para>  The type name <type>varchar</type> is an alias for <type>character  varying</type>, while <type>bpchar</type> (with length specifier) and  <type>char</type> are aliases for <type>character</type>. The  <type>varchar<…
- `[]`
  > according to the database's selected character set encoding.  Second, operations on binary strings process the actual bytes,  whereas the processing of character strings depends on locale settings.  In short, binary strings are appropriate for storing data that the  programmer thinks of as <quote>raw bytes</quote>, whereas character  strings are appropriate for storing text.  </para>  <para>  The <type>bytea</type> type supports two  formats for input and output: <quote>hex</quote> format  and <productname>PostgreSQL</productname>'s historical  <quote>escape</quote> format. Both  of these are…

`verdict_relevant:` ______   `notes:` ______

---

## 148. `532ae0efffbd7f1e`  (docker)

**Query** (en / concept): What is a container, and how does it differ from a virtual machine?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/get-started/docker-concepts/the-basics/what-is-a-container.md`

**Claimed section** (unverified): `['What is a container?']`

**Actual sections in the index:**

- `[]`
  > {{< youtube-embed W1kWqFkiu7k >}}
- `['Explanation']`
  > Explanation  Imagine you're developing a killer web app that has three main components - a React frontend, a Python API, and a PostgreSQL database. If you wanted to work on this project, you'd have to install Node, Python, and PostgreSQL.  How do you make sure you have the same versions as the other developers on your team? Or your CI/CD system? Or what's used in production?  How do you ensure the version of Python (or Node or the database) your app needs isn't affected by what's already on your machine? How do you manage potential conflicts?  Enter containers!  What is a container? Simply put…
- `['Explanation', 'Containers versus virtual machines (VMs)']`
  > Containers versus virtual machines (VMs)  Without getting too deep, a VM is an entire operating system with its own kernel, hardware drivers, programs, and applications. Spinning up a VM only to isolate a single application is a lot of overhead.  A container is simply an isolated process with all of the files it needs to run. If you run multiple containers, they all share the same kernel, allowing you to run more applications on less infrastructure.  > **Using VMs and containers together**  >  > Quite often, you will see containers and VMs used together. As an example, in a cloud environment,…
- `['Explanation', 'Try it out']`
  > Try it out  In this hands-on, you will see how to run a Docker container using the Docker Desktop GUI.  {{< tabs group=concept-usage persist=true >}}  {{< tab name="Using the GUI" >}}  Use the following instructions to run a container.  1. Open Docker Desktop and select the **Search** field on the top navigation bar.  2. Specify `welcome-to-docker` in the search input and then select the **Pull** button.  ![A screenshot of the Docker Desktop Dashboard showing the search result for welcome-to-docker Docker image ](images/search-the-docker-image.webp?border=true&w=1000&h=700)  3. Once the image…
- `['Explanation', 'Try it out', 'View your container']`
  > View your container  You can view all of your containers by going to the **Containers** view of the Docker Desktop Dashboard.  ![Screenshot of the container view of the Docker Desktop GUI showing the welcome-to-docker container running on the host port 8080](images/view-your-containers.webp?border=true&w=750&h=600)  This container runs a web server that displays a simple website. When working with more complex projects, you'll run different parts in different containers. For example, you might run a different container for the frontend, backend, and database.
- `['Explanation', 'Try it out', 'Access the frontend']`
  > Access the frontend  When you launched the container, you exposed one of the container's ports onto your machine. Think of this as creating configuration to let you connect through the isolated environment of the container.  For this container, the frontend is accessible on port `8080`. To open the website, select the link in the **Port(s)** column of your container or visit [http://localhost:8080](http://localhost:8080) in your browser.  ![Screenshot of the landing page coming from the running container](images/access-the-frontend.webp?border)

`verdict_relevant:` ______   `notes:` ______

---

## 149. `0f0ea880fa936a6a`  (docker)

**Query** (zh / code_api): 怎么用 BuildKit 的构建密钥（build secrets）避免把密码写进镜像？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/build/building/secrets.md`

**Claimed section** (unverified): `['Build secrets']`

**Actual sections in the index:**

- `[]`
  > A build secret is any piece of sensitive information, such as a password or API  token, consumed as part of your application's build process.  Build arguments and environment variables are inappropriate for passing secrets  to your build, because they persist in the final image. Instead, you should use  secret mounts or SSH mounts, which expose secrets to your builds securely.
- `['Types of build secrets']`
  > Types of build secrets  - [Secret mounts](#secret-mounts) are general-purpose mounts for passing  secrets into your build. A secret mount takes a secret from the build client  and makes it temporarily available inside the build container, for the  duration of the build instruction. This is useful if, for example, your build  needs to communicate with a private artifact server or API.  - [SSH mounts](#ssh-mounts) are special-purpose mounts for making SSH sockets  or keys available inside builds. They're commonly used when you need to fetch  private Git repositories in your builds.  - [Git authe…
- `['Types of build secrets', 'Using build secrets']`
  > Using build secrets  For secret mounts and SSH mounts, using build secrets is a two-step process.  First you need to pass the secret into the `docker build` command, and then you  need to consume the secret in your Dockerfile.  To pass a secret to a build, use the [`docker build --secret`  flag](/reference/cli/docker/buildx/build/#secret), or the  equivalent options for [Bake](../bake/reference.md#targetsecret).  {{< tabs >}}  {{< tab name="CLI" >}}  $ docker build --secret id=aws,src=$HOME/.aws/credentials .  {{< /tab >}}  {{< tab name="Bake" >}}  variable "HOME" {  default = null  }  target…
- `['Types of build secrets', 'Secret mounts']`
  > Secret mounts  Secret mounts expose secrets to the build containers, as files or environment  variables. You can use secret mounts to pass sensitive information to your  builds, such as API tokens, passwords, or SSH keys.
- `['Types of build secrets', 'Secret mounts', 'Sources']`
  > Sources  The source of a secret can be either a  [file](/reference/cli/docker/buildx/build/#file) or an  [environment variable](/reference/cli/docker/buildx/build/#typeenv).  When you use the CLI or Bake, the type can be detected automatically. You can  also specify it explicitly with `type=file` or `type=env`.  The following example mounts the environment variable `KUBECONFIG` to secret ID `kube`,  as a file in the build container at `/run/secrets/kube`.  $ docker build --secret id=kube,env=KUBECONFIG .  When you use secrets from environment variables, you can omit the `env` parameter  to bin…
- `['Types of build secrets', 'Secret mounts', 'Target']`
  > Target  When consuming a secret in a Dockerfile, the secret is mounted to a file by  default. The default file path of the secret, inside the build container, is  `/run/secrets/<id>`. You can customize how the secrets get mounted in the build  container using the `target` and `env` options for the `RUN --mount` flag in  the Dockerfile.  The following example takes secret id `aws` and mounts it to a file at  `/run/secrets/aws` in the build container.  RUN --mount=type=secret,id=aws \  AWS_SHARED_CREDENTIALS_FILE=/run/secrets/aws \  aws s3 cp ...  To mount a secret as a file with a different nam…

`verdict_relevant:` ______   `notes:` ______

---

## 150. `914a69e26b18c683`  (postgresql)

**Query** (zh / concept): PostgreSQL 的事务隔离级别有哪几种，各自有什么特点？

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/mvcc.sgml`

**Claimed section** (unverified): `['Concurrency Control', 'Transaction Isolation']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/mvcc.sgml -->  <chapter id="mvcc">  <title>Concurrency Control</title>  <indexterm>  <primary>concurrency</primary>  </indexterm>  <para>  This chapter describes the behavior of the  <productname>PostgreSQL</productname> database system when two or  more sessions try to access the same data at the same time. The  goals in that situation are to allow efficient access for all  sessions while maintaining strict data integrity. Every developer  of database applications should be familiar with the topics covered  in this chapter.  </para>  <sect1 id="mvcc-intro">  <title>Introduct…
- `[]`
  > data or changes committed by concurrent transactions during the query's  execution. In effect, a <command>SELECT</command> query sees  a snapshot of the database as of the instant the query begins to  run. However, <command>SELECT</command> does see the effects  of previous updates executed within its own transaction, even  though they are not yet committed. Also note that two successive  <command>SELECT</command> commands can see different data, even  though they are within a single transaction, if other transactions  commit changes after the first <command>SELECT</command> starts and  before…
- `[]`
  > The <command>DELETE</command> will have no effect even though  there is a <literal>website.hits = 10</literal> row before and  after the <command>UPDATE</command>. This occurs because the  pre-update row value <literal>9</literal> is skipped, and when the  <command>UPDATE</command> completes and <command>DELETE</command>  obtains a lock, the new row value is no longer <literal>10</literal> but  <literal>11</literal>, which no longer matches the criteria.  </para>  <para>  Because Read Committed mode starts each command with a new snapshot  that includes all transactions committed up to that in…
- `[]`
  > execution of a concurrent set of serializable transactions behave  in a manner inconsistent with all possible serial (one at a time)  executions of those transactions. This monitoring does not  introduce any blocking beyond that present in repeatable read, but  there is some overhead to the monitoring, and detection of the  conditions which could cause a  <firstterm>serialization anomaly</firstterm> will trigger a  <firstterm>serialization failure</firstterm>.  </para>  <para>  As an example,  consider a table <structname>mytab</structname>, initially containing:  <screen>  class | value  ----…
- `[]`
  > conflicting keys explicitly check if they can do so first. For example,  imagine an application that asks the user for a new key and then checks  that it doesn't exist already by trying to select it first, or generates  a new key by selecting the maximum existing key and adding one. If some  Serializable transactions insert new keys directly without following this  protocol, unique constraints violations might be reported even in cases  where they could not occur in a serial execution of the concurrent  transactions.  </para>  <para>  For optimal performance when relying on Serializable transa…
- `[]`
  > (for full details see the documentation of these commands).  </para>  </listitem>  </varlistentry>  <varlistentry>  <term>  <literal>SHARE</literal> (<literal>ShareLock</literal>)  </term>  <listitem>  <para>  Conflicts with the <literal>ROW EXCLUSIVE</literal>,  <literal>SHARE UPDATE EXCLUSIVE</literal>, <literal>SHARE ROW  EXCLUSIVE</literal>, <literal>EXCLUSIVE</literal>, and  <literal>ACCESS EXCLUSIVE</literal> lock modes.  This mode protects a table against concurrent data changes.  </para>  <para>  Acquired by <command>CREATE INDEX</command>  (without <option>CONCURRENTLY</option>).  </p…

`verdict_relevant:` ______   `notes:` ______

---

## 151. `2d6b4a04379f9e79`  (python)

**Query** (en / code_api): What does the built-in enumerate function do and how is it used with a for loop?

**Document**: `python@96ebb20fc2f0542d9387091e49626f9a1132de82:Doc/library/functions.rst`

**Claimed section** (unverified): `['Built-in Functions']`

**Actual sections in the index:**

- `[]`
  > .. XXX document all delegations to __special__ methods  .. _built-in-funcs:  Built-in Functions  ==================  The Python interpreter has a number of functions and types built into it that  are always available. They are listed here in alphabetical order.  +---------------------------------------------------------------------------------------------------+  | Built-in Functions |  +=========================+=======================+=======================+=========================+  | | **A** | | **E** | | **L** | | **R** |  | | :func:`abs` | | :func:`enumerate` | | :func:`len` | | |func-…
- `[]`
  > See :func:`sys.breakpointhook` for usage details.  Note that this is not guaranteed if :func:`sys.breakpointhook`  has been replaced.  .. audit-event:: builtins.breakpoint breakpointhook breakpoint  .. versionadded:: 3.7  .. _func-bytearray:  .. class:: bytearray(source=b'')  bytearray(source, encoding, errors='strict')  :noindex:  Return a new array of bytes. The :class:`bytearray` class is a mutable  sequence of integers in the range 0 <= x < 256. It has most of the usual  methods of mutable sequences, described in :ref:`typesseq-mutable`, as well  as most methods that the :class:`bytes` typ…
- `[]`
  > If you want to parse Python code into its AST representation, see  :func:`ast.parse`.  .. audit-event:: compile source,filename compile  Raises an :ref:`auditing event <auditing>` ``compile`` with arguments  ``source`` and ``filename``. This event may also be raised by implicit  compilation.  .. note::  When compiling a string with multi-line code in ``'single'`` or  ``'eval'`` mode, input must be terminated by at least one newline  character. This is to facilitate detection of incomplete and complete  statements in the :mod:`code` module.  .. warning::  It is possible to crash the Python inte…
- `[]`
  > than it tries to supply a rigorously or consistently defined set of names,  and its detailed behavior may change across releases. For example,  metaclass attributes are not in the result list when the argument is a  class.  .. function:: divmod(a, b, /)  Take two (non-complex) numbers as arguments and return a pair of numbers  consisting of their quotient and remainder when using integer division. With  mixed operand types, the rules for binary arithmetic operators apply. For  integers, the result is the same as ``(a // b, a % b)``. For floating-point  numbers the result is ``(q, a % b)``, whe…
- `[]`
  > names, but this is **not** a security mechanism: the executed code can  still access all builtins.  The *closure* argument specifies a closure--a tuple of cellvars.  It's only valid when the *object* is a code object containing  :term:`free (closure) variables <closure variable>`.  The length of the tuple must exactly match the length of the code object's  :attr:`~codeobject.co_freevars` attribute.  .. audit-event:: exec code_object exec  Raises an :ref:`auditing event <auditing>` ``exec`` with the code object  as the argument. Code compilation events may also be raised.  .. note::  The built-…
- `[]`
  > The arguments are an object and a string. The result is ``True`` if the  string is the name of one of the object's attributes, ``False`` if not. (This  is implemented by calling ``getattr(object, name)`` and seeing whether it  raises an :exc:`AttributeError` or not.)  .. function:: hash(object, /)  Return the hash value of the object (if it has one). Hash values are  integers. They are used to quickly compare dictionary keys during a  dictionary lookup. Numeric values that compare equal have the same hash  value (even if they are of different types, as is the case for 1 and 1.0).  .. note::  F…

`verdict_relevant:` ______   `notes:` ______

---

## 152. `a508b0281996bb17`  (docker)

**Query** (zh / config): Compose 里的 secrets 怎么定义和使用？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/compose/how-tos/use-secrets.md`

**Claimed section** (unverified): `['Manage secrets securely in Docker Compose']`

**Actual sections in the index:**

- `[]`
  > A secret is any piece of data, such as a password, certificate, or API key, that shouldn’t be transmitted over a network or stored unencrypted in a Dockerfile or in your application’s source code.  {{% include "compose/secrets.md" %}}  Environment variables are often available to all processes, and it can be difficult to track access. They can also be printed in logs when debugging errors without your knowledge. Using secrets mitigates these risks.
- `['Use secrets']`
  > Use secrets  Secrets are mounted as a file in `/run/secrets/<secret_name>` inside the container.  Getting a secret into a container is a two-step process. First, define the secret using the [top-level secrets element in your Compose file](/reference/compose-file/secrets.md). Next, update your service definitions to reference the secrets they require with the [secrets attribute](/reference/compose-file/services.md#secrets). Compose grants access to secrets on a per-service basis.  Unlike the other methods, this permits granular access control within a service container via standard filesystem p…
- `['Use secrets', 'Examples']`
  > Examples
- `['Use secrets', 'Examples', 'Single-service secret injection']`
  > Single-service secret injection  In the following example, the frontend service is given access to the `my_secret` secret. In the container, `/run/secrets/my_secret` is set to the contents of the file `./my_secret.txt`.  services:  myapp:  image: myapp:latest  secrets:  - my_secret  secrets:  my_secret:  file: ./my_secret.txt
- `['Use secrets', 'Examples', 'Multi-service secret sharing and password management']`
  > Multi-service secret sharing and password management  services:  db:  image: mysql:latest  volumes:  - db_data:/var/lib/mysql  environment:  MYSQL_ROOT_PASSWORD_FILE: /run/secrets/db_root_password  MYSQL_DATABASE: wordpress  MYSQL_USER: wordpress  MYSQL_PASSWORD_FILE: /run/secrets/db_password  secrets:  - db_root_password  - db_password  wordpress:  depends_on:  - db  image: wordpress:latest  ports:  - "8000:80"  environment:  WORDPRESS_DB_HOST: db:3306  WORDPRESS_DB_USER: wordpress  WORDPRESS_DB_PASSWORD_FILE: /run/secrets/db_password  secrets:  - db_password  secrets:  db_password:  file: db…
- `['Use secrets', 'Examples', 'Build secrets']`
  > Build secrets  In the following example, the `npm_token` secret is made available at build time. Its value is taken from the `NPM_TOKEN` environment variable.  services:  myapp:  build:  secrets:  - npm_token  context: .  secrets:  npm_token:  environment: NPM_TOKEN

`verdict_relevant:` ______   `notes:` ______

---

## 153. `e4bcb7ee1b7f5afd`  (postgresql)

**Query** (en / troubleshooting): How do I find out which queries are currently running on the server?

**Document**: `postgresql@7a0299a1348b563c72a57a2a40462e90af9dfbac:doc/src/sgml/monitoring.sgml`

**Claimed section** (unverified): `['Monitoring Database Activity']`

**Actual sections in the index:**

- `[]`
  > <!-- doc/src/sgml/monitoring.sgml -->  <chapter id="monitoring">  <title>Monitoring Database Activity</title>  <indexterm zone="monitoring">  <primary>monitoring</primary>  <secondary>database activity</secondary>  </indexterm>  <indexterm zone="monitoring">  <primary>database activity</primary>  <secondary>monitoring</secondary>  </indexterm>  <para>  A database administrator frequently wonders, <quote>What is the system  doing right now?</quote>  This chapter discusses how to find that out.  </para>  <para>  Several tools are available for monitoring database activity and  analyzing performa…
- `[]`
  > </para>  <para>  Cumulative statistics are collected in shared memory. Every  <productname>PostgreSQL</productname> process collects statistics locally,  then updates the shared data at appropriate intervals. When a server,  including a physical replica, shuts down cleanly, a permanent copy of the  statistics data is stored in the <filename>pg_stat</filename> subdirectory,  so that statistics can be retained across server restarts. In contrast,  when starting from an unclean shutdown (e.g., after an immediate shutdown,  a server crash, starting from a base backup, and point-in-time recovery),…
- `[]`
  > <entry>View Name</entry>  <entry>Description</entry>  </row>  </thead>  <tbody>  <!-- everything related to global objects, alphabetically -->  <row>  <entry><structname>pg_stat_archiver</structname><indexterm><primary>pg_stat_archiver</primary></indexterm></entry>  <entry>One row only, showing statistics about the  WAL archiver process's activity. See  <link linkend="monitoring-pg-stat-archiver-view">  <structname>pg_stat_archiver</structname></link> for details.  </entry>  </row>  <row>  <entry><structname>pg_stat_bgwriter</structname><indexterm><primary>pg_stat_bgwriter</primary></indexterm…
- `[]`
  > process is a parallel group leader or leader apply worker, or does not  participate in any parallel operation.  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>usesysid</structfield> <type>oid</type>  </para>  <para>  OID of the user logged into this backend  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>usename</structfield> <type>name</type>  </para>  <para>  Name of the user logged into this backend  </para></entry>  </row>  <row>  <entry role="catalog_table_entr…
- `[]`
  > a backend process to perform operations in parallel.  </para>  </listitem>  <listitem>  <para>  <literal>REPACK decoding worker</literal>: A background process that  decodes WAL for <command>REPACK (CONCURRENTLY)</command>.  </para>  </listitem>  <listitem>  <para>  <literal>slotsync worker</literal>: The background process that  synchronizes logical replication slots on a streaming replication  standby server, active when <xref linkend="guc-sync-replication-slots"/>  is set to <literal>on</literal>.  </para>  </listitem>  <listitem>  <para>  <literal>standalone backend</literal>: The backend…
- `[]`
  > reverse DNS lookup of <structfield>client_addr</structfield>. This field will  only be non-null for IP connections, and only when <xref linkend="guc-log-hostname"/> is enabled.  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>client_port</structfield> <type>integer</type>  </para>  <para>  TCP port number that the client is using for communication  with this WAL sender, or <literal>-1</literal> if a Unix socket is used  </para></entry>  </row>  <row>  <entry role="catalog_table_entry"><para role="column_definition">  <structfield>…

`verdict_relevant:` ______   `notes:` ______

---

## 154. `e0ef9a4c70dbcbf8`  (docker)

**Query** (zh / concept): Docker 镜像和容器是什么关系，镜像是怎么构成的？

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/get-started/docker-concepts/the-basics/what-is-an-image.md`

**Claimed section** (unverified): `['What is an image?']`

**Actual sections in the index:**

- `[]`
  > {{< youtube-embed NyvT9REqLe4 >}}
- `['Explanation']`
  > Explanation  Seeing as a [container](./what-is-a-container.md) is an isolated process, where does it get its files and configuration? How do you share those environments?  That's where container images come in. A container image is a standardized package that includes all of the files, binaries, libraries, and configurations to run a container.  For a [PostgreSQL](https://hub.docker.com/_/postgres) image, that image will package the database binaries, config files, and other dependencies. For a Python web app, it'll include the Python runtime, your app code, and all of its dependencies.  There…
- `['Explanation', 'Finding images']`
  > Finding images  [Docker Hub](https://hub.docker.com) is the default global marketplace for storing and distributing images. It has over 100,000 images created by developers that you can run locally. You can search for Docker Hub images and run them directly from Docker Desktop.  Docker Hub provides a variety of Docker-supported and endorsed images known as Docker Trusted Content. These provide fully managed services or great starters for your own images. These include:  - [Docker Official Images](https://hub.docker.com/search?badges=official) - a curated set of Docker repositories, serve as th…
- `['Explanation', 'Try it out']`
  > Try it out  {{< tabs group=concept-usage persist=true >}}  {{< tab name="Using the GUI" >}}  In this hands-on, you will learn how to search and pull a container image using the Docker Desktop GUI.
- `['Explanation', 'Try it out', 'Search for and download an image']`
  > Search for and download an image  1. Open the Docker Desktop Dashboard and select the **Images** view in the left-hand navigation menu.  ![A screenshot of the Docker Desktop Dashboard showing the image view on the left sidebar](images/click-image.webp?border=true&w=1050&h=400)  2. Select the **Search images to run** button. If you don't see it, select the _global search bar_ at the top of the screen.  ![A screenshot of the Docker Desktop Dashboard showing the search ta](images/search-image.webp?border)  3. In the **Search** field, enter "welcome-to-docker". Once the search has completed, selec…
- `['Explanation', 'Try it out', 'Learn about the image']`
  > Learn about the image  Once you have an image downloaded, you can learn quite a few details about the image either through the GUI or the CLI.  1. In the Docker Desktop Dashboard, select the **Images** view.  2. Select the **docker/welcome-to-docker** image to open details about the image.  ![A screenshot of the Docker Desktop Dashboard showing the images view with an arrow pointing to the docker/welcome-to-docker image](images/pulled-image.webp?border=true&w=1050&h=400)  3. The image details page presents you with information regarding the layers of the image, the packages and libraries insta…

`verdict_relevant:` ______   `notes:` ______

---

## 155. `bcecd9849d5fc48b`  (kubernetes)

**Query** (zh / config): 怎么给 Pod 配置存活、就绪和启动探针？

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes.md`

**Claimed section** (unverified): `['Configure Liveness, Readiness and Startup Probes']`

**Actual sections in the index:**

- `[]`
  > This page shows how to configure liveness, readiness and startup probes for  containers.  For more information about probes, see  [Liveness, Readiness and Startup Probes](/docs/concepts/workloads/pods/probes).
- `['{{% heading "prerequisites" %}}']`
  > {{% heading "prerequisites" %}}  {{< include "task-tutorial-prereqs.md" >}}
- `['{{% heading "prerequisites" %}}', 'Define a liveness command']`
  > Define a liveness command  Many applications running for long periods of time eventually transition to  broken states, and cannot recover except by being restarted. Kubernetes provides  liveness probes to detect and remedy such situations.  In this exercise, you create a Pod that runs a container based on the  `registry.k8s.io/busybox:1.27.2` image. Here is the configuration file for the Pod:  {{% code_sample file="pods/probe/exec-liveness.yaml" %}}  In the configuration file, you can see that the Pod has a single `Container`.  The `periodSeconds` field specifies that the kubelet should perfor…
- `['{{% heading "prerequisites" %}}', 'Define a liveness HTTP request']`
  > Define a liveness HTTP request  Another kind of liveness probe uses an HTTP GET request. Here is the configuration  file for a Pod that runs a container based on the `registry.k8s.io/e2e-test-images/agnhost` image.  {{% code_sample file="pods/probe/http-liveness.yaml" %}}  In the configuration file, you can see that the Pod has a single container.  The `periodSeconds` field specifies that the kubelet should perform a liveness  probe every 3 seconds. The `initialDelaySeconds` field tells the kubelet that it  should wait 3 seconds before performing the first probe. To perform a probe, the  kubel…
- `['{{% heading "prerequisites" %}}', 'Define a TCP liveness probe']`
  > Define a TCP liveness probe  A third type of liveness probe uses a TCP socket. With this configuration, the  kubelet will attempt to open a socket to your container on the specified port.  If it can establish a connection, the container is considered healthy, if it  can't it is considered a failure.  {{% code_sample file="pods/probe/tcp-liveness-readiness.yaml" %}}  As you can see, configuration for a TCP check is quite similar to an HTTP check.  This example uses both readiness and liveness probes. The kubelet will run the  first liveness probe 15 seconds after the container starts. This will…
- `['{{% heading "prerequisites" %}}', 'Define a gRPC liveness probe']`
  > Define a gRPC liveness probe  {{< feature-state for_k8s_version="v1.27" state="stable" >}}  If your application implements the  [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md),  this example shows how to configure Kubernetes to use it for application liveness checks.  Similarly you can configure readiness and startup probes.  Here is an example manifest:  {{% code_sample file="pods/probe/grpc-liveness.yaml" %}}  To try the gRPC liveness check, create a Pod using the command below.  In the example below, the etcd pod is configured to use gRPC liv…

`verdict_relevant:` ______   `notes:` ______

---

## 156. `4d38aa0d5ac2285e`  (docker)

**Query** (en / config): How do you configure the daemon's logging driver and its options?

**Document**: `docker@bb6ca1cb679394b4e9f7f44cc84a1288f8966247:content/manuals/engine/logging/configure.md`

**Claimed section** (unverified): `['Configure logging drivers']`

**Actual sections in the index:**

- `[]`
  > Docker includes multiple logging mechanisms to help you get information from  running containers and services. These mechanisms are called logging drivers.  Each Docker daemon has a default logging driver, which each container uses  unless you configure it to use a different logging driver, or log driver for  short.  As a default, Docker uses the [`json-file` logging driver](drivers/json-file.md), which  caches container logs as JSON internally. In addition to using the logging drivers  included with Docker, you can also implement and use [logging driver plugins](plugins.md).  > [!TIP]  >  > U…
- `['Configure the default logging driver']`
  > Configure the default logging driver  To configure the Docker daemon to default to a specific logging driver, set the  value of `log-driver` to the name of the logging driver in the `daemon.json`  configuration file. Refer to the "daemon configuration file" section in the  [`dockerd` reference manual](/reference/cli/dockerd/#daemon-configuration-file)  for details.  The default logging driver is `json-file`. The following example sets the default  logging driver to the [`local` log driver](drivers/local.md):  {  "log-driver": "local"  }  If the logging driver has configurable options, you can…
- `['Configure the default logging driver', 'Configure the logging driver for a container']`
  > Configure the logging driver for a container  When you start a container, you can configure it to use a different logging  driver than the Docker daemon's default, using the `--log-driver` flag. If the  logging driver has configurable options, you can set them using one or more  instances of the `--log-opt <NAME>=<VALUE>` flag. Even if the container uses the  default logging driver, it can use different configurable options.  The following example starts an Alpine container with the `none` logging driver.  $ docker run -it --log-driver none alpine ash  To find the current logging driver for a…
- `['Configure the default logging driver', 'Configure the delivery mode of log messages from container to log driver']`
  > Configure the delivery mode of log messages from container to log driver  Docker provides two modes for delivering messages from the container to the log  driver:  - (default) direct, blocking delivery from container to driver  - non-blocking delivery that stores log messages in an intermediate per-container buffer for consumption by driver  The `non-blocking` message delivery mode prevents applications from blocking due  to logging back pressure. Applications are likely to fail in unexpected ways when  STDERR or STDOUT streams block.  > [!WARNING]  >  > When the buffer is full, new messages w…
- `['Configure the default logging driver', 'Configure the delivery mode of log messages from container to log driver', 'Use environment variables or labels with logging drivers']`
  > Use environment variables or labels with logging drivers  Some logging drivers add the value of a container's `--env|-e` or `--label`  flags to the container's logs. This example starts a container using the Docker  daemon's default logging driver (in the following example, `json-file`) but  sets the environment variable `os=ubuntu`.  $ docker run -dit --label production_status=testing -e os=ubuntu alpine sh  If the logging driver supports it, this adds additional fields to the logging  output. The following output is generated by the `json-file` logging driver:  "attrs":{"production_status":"…
- `['Configure the default logging driver', 'Supported logging drivers']`
  > Supported logging drivers  The following logging drivers are supported. See the link to each driver's  documentation for its configurable options, if applicable. If you are using  [logging driver plugins](plugins.md), you may  see more options.  | Driver                                | Description                                                                                                 |  | :------------------------------------ | :---------------------------------------------------------------------------------------------------------- |  | `none`                                | No log…

`verdict_relevant:` ______   `notes:` ______

---

## 157. `0dc97fae409e9c95`  (kubernetes)

**Query** (en / concept): What are controllers in Kubernetes and how do they maintain desired state?

**Document**: `kubernetes@b035ea80a2f666e0a60923560984458806788104:content/en/docs/concepts/architecture/controller.md`

**Claimed section** (unverified): `['Controllers']`

**Actual sections in the index:**

- `[]`
  > In robotics and automation, a _control loop_ is  a non-terminating loop that regulates the state of a system.  Here is one example of a control loop: a thermostat in a room.  When you set the temperature, that's telling the thermostat  about your *desired state*. The actual room temperature is the  *current state*. The thermostat acts to bring the current state  closer to the desired state, by turning equipment on or off.  {{< glossary_definition term_id="controller" length="short">}}
- `['Controller pattern']`
  > Controller pattern  A controller tracks at least one Kubernetes resource type.  These {{< glossary_tooltip text="objects" term_id="object" >}}  have a spec field that represents the desired state. The  controller(s) for that resource are responsible for making the current  state come closer to that desired state.  The controller might carry the action out itself; more commonly, in Kubernetes,  a controller will send messages to the  {{< glossary_tooltip text="API server" term_id="kube-apiserver" >}} that have  useful side effects. You'll see examples of this below.  {{< comment >}}  Some built…
- `['Controller pattern', 'Control via API server']`
  > Control via API server  The {{< glossary_tooltip term_id="job" >}} controller is an example of a  Kubernetes built-in controller. Built-in controllers manage state by  interacting with the cluster API server.  Job is a Kubernetes resource that runs a  {{< glossary_tooltip term_id="pod" >}}, or perhaps several Pods, to carry out  a task and then stop.  (Once [scheduled](/docs/concepts/scheduling-eviction/), Pod objects become part of the  desired state for a kubelet).  When the Job controller sees a new task it makes sure that, somewhere  in your cluster, the kubelets on a set of Nodes are runn…
- `['Controller pattern', 'Control via API server', 'Direct control']`
  > Direct control  In contrast with Job, some controllers need to make changes to  things outside of your cluster.  For example, if you use a control loop to make sure there  are enough {{< glossary_tooltip text="Nodes" term_id="node" >}}  in your cluster, then that controller needs something outside the  current cluster to set up new Nodes when needed.  Controllers that interact with external state find their desired state from  the API server, then communicate directly with an external system to bring  the current state closer in line.  (There actually is a [controller](https://github.com/kuber…
- `['Controller pattern', 'Desired versus current state {#desired-vs-current}']`
  > Desired versus current state {#desired-vs-current}  Kubernetes takes a cloud-native view of systems, and is able to handle  constant change.  Your cluster could be changing at any point as work happens and  control loops automatically fix failures. This means that,  potentially, your cluster never reaches a stable state.  As long as the controllers for your cluster are running and able to make  useful changes, it doesn't matter if the overall state is stable or not.
- `['Controller pattern', 'Design']`
  > Design  As a tenet of its design, Kubernetes uses lots of controllers that each manage  a particular aspect of cluster state. Most commonly, a particular control loop  (controller) uses one kind of resource as its desired state, and has a different  kind of resource that it manages to make that desired state happen. For example,  a controller for Jobs tracks Job objects (to discover new work) and Pod objects  (to run the Jobs, and then to see when the work is finished). In this case  something else creates the Jobs, whereas the Job controller creates Pods.  It's useful to have simple controlle…

`verdict_relevant:` ______   `notes:` ______

---
