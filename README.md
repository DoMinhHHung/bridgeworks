## BrigdeWorks
# Private Talent Liquidity Network

> A private exchange for verified software talent.

Nền tảng giúp software agency và công ty công nghệ luân chuyển developer đã được kiểm chứng, thay vì tiếp tục phụ thuộc vào job board, CV spam và quy trình tuyển dụng thiếu minh bạch.

Sản phẩm kết hợp bốn thành phần:

1. **B2B Bench Exchange** — agency đưa developer đang bench vào một private talent pool.
2. **Verified Hiring Intent** — chỉ employer có nhu cầu tuyển thật mới được tiếp cận talent.
3. **Final-Round Candidate Exchange** — tái sử dụng những candidate đã vượt qua nhiều vòng phỏng vấn nhưng chưa được tuyển.
4. **Portable Proof-of-Work Passport** — lưu trữ bằng chứng năng lực có thể xác minh và mang theo giữa các công ty.

---

## 1. Product Thesis

Thị trường tuyển dụng hiện tại tối ưu cho số lượng:

- Nhiều job posting.
- Nhiều application.
- Nhiều CV.
- Nhiều recruiter outreach.

Nhưng lại không tối ưu cho:

- Hiring intent thật.
- Khả năng phản hồi.
- Bằng chứng năng lực đáng tin.
- Khả năng tái sử dụng dữ liệu tuyển dụng.
- Việc luân chuyển talent đang bị sử dụng kém hiệu quả.

Sản phẩm này đi theo hướng ngược lại:

> Ít job hơn, ít candidate hơn, nhưng mỗi bên đều đã được xác minh và có xác suất giao dịch cao hơn.

---

## 2. Problem Statement

### 2.1. Software agency có developer đang bench

Agency vẫn phải trả lương nhưng không tạo được doanh thu từ developer đang chờ project.

Các phương án hiện tại thường là:

- Chờ sales team tìm project mới.
- Cho nghỉ việc.
- Đăng profile thủ công trong các group kín.
- Gửi spreadsheet qua lại giữa các agency.

Những cách này thiếu:

- Chuẩn dữ liệu chung.
- Verification.
- Contract workflow.
- Availability tracking.
- Trust history.

### 2.2. Employer không biết candidate nào thực sự đáng tin

CV và profile tự khai khó phản ánh chính xác:

- Candidate đã thực sự làm gì.
- Seniority trong môi trường production.
- Mức độ ownership.
- Chất lượng làm việc trong project trước.
- Kết quả interview hoặc trial trước đó.

### 2.3. Candidate gặp job giả và recruiter spam

Nhiều job không có:

- Approved headcount.
- Salary range rõ ràng.
- Hiring manager thật.
- Start date.
- Interview SLA.
- Cam kết phản hồi.

### 2.4. Dữ liệu tuyển dụng bị vứt bỏ

Một candidate có thể vượt qua technical interview và final round nhưng không được tuyển do:

- Hiring freeze.
- Salary mismatch.
- Timing.
- Domain mismatch.
- Một candidate khác phù hợp hơn.

Toàn bộ tín hiệu tích cực này thường không được tái sử dụng.

---

## 3. Product Vision

Xây dựng một **trusted liquidity network** cho verified software talent.

Trong hệ thống này:

- Talent có thể được luân chuyển giữa các agency.
- Employer chỉ được tiếp cận talent khi hiring intent đã được xác minh.
- Candidate sở hữu Proof-of-Work Passport của mình.
- Interview outcome và project outcome có thể tạo thành verified evidence.
- Mỗi giao dịch thành công làm mạng lưới đáng tin hơn.

---

## 4. Core Product Model

```text
B2B Bench Exchange             ┐
                               ├── Verified Talent Supply
Final-Round Candidate Exchange ┘

Verified Hiring Intent         ─── Qualified Demand

Proof-of-Work Passport         ─── Trust Infrastructure
```

### 4.1. B2B Bench Exchange

Agency có thể đưa developer đang bench vào private exchange.

Thông tin chính:

- Role.
- Seniority.
- Tech stack.
- Availability.
- Engagement model.
- Monthly rate hoặc hourly rate.
- Timezone.
- English level.
- Project evidence.
- Agency reference.

Các hình thức giao dịch:

- Staff augmentation.
- Project-based contract.
- Short-term replacement.
- Contract-to-hire.

### 4.2. Verified Hiring Intent

Employer muốn truy cập talent phải xác minh:

- Legal organization.
- Hiring manager.
- Approved headcount.
- Compensation range hoặc project budget.
- Expected start date.
- Interview process.
- Response SLA.
- Contract type.

Có thể sử dụng:

- Membership.
- Deposit.
- Manual approval.
- Trust score nội bộ.

Mục tiêu không phải tối đa số job posting.

Mục tiêu là:

> Mỗi hiring intent trên hệ thống đều có xác suất giao dịch thật cao.

### 4.3. Final-Round Candidate Exchange

Candidate đã đi sâu trong một hiring process có thể được đưa vào private pool khi:

- Candidate đồng ý.
- Employer đồng ý chia sẻ structured outcome.
- Không chia sẻ raw interview notes.
- Không chia sẻ dữ liệu nhạy cảm ngoài phạm vi được cho phép.

Structured signals có thể gồm:

- Passed recruiter screening.
- Passed coding interview.
- Passed system design.
- Strong communication.
- Strong technical fundamentals.
- Rejected due to compensation mismatch.
- Rejected due to domain mismatch.
- Hiring paused.
- Another candidate selected.

### 4.4. Portable Proof-of-Work Passport

Passport là tập hợp những claim có nguồn phát hành và trạng thái verification.

Ví dụ:

```text
Agency X verified:
- Worked on a payment service.
- Used Go, PostgreSQL and Kafka.
- Owned deployment and incident response.
- Operated a production system at 2,000 requests/second.
```

Passport có thể chứa:

- Identity verification.
- Employment verification.
- Project history.
- Verified tech stack.
- Role and ownership.
- Production exposure.
- Interview outcomes.
- Trial outcomes.
- Contract outcomes.
- References.
- Supporting artifacts.

Không tạo một điểm số tổng duy nhất cho con người.

Thay vào đó, hệ thống lưu nhiều claim độc lập với:

- Issuer.
- Subject.
- Evidence type.
- Verification level.
- Visibility.
- Validity period.

---

## 5. Initial Market Wedge

### Target customer

Software agency có:

- 20–300 nhân sự kỹ thuật.
- Developer thường xuyên bị bench.
- Khả năng làm remote hoặc cross-company contract.
- Nhu cầu bổ sung talent ngắn hạn.

### Initial talent segment

Chỉ bắt đầu với:

- Backend Engineer.
- DevOps / Platform Engineer.
- Mid-level trở lên.
- Contract từ 1–6 tháng.
- Việt Nam hoặc một geography cụ thể.

### Initial value proposition

> Biến developer đang bench thành doanh thu trong vài ngày, đồng thời giúp agency tìm verified engineers từ network đáng tin.

### Không làm trong giai đoạn đầu

- Public job board.
- Generic freelancer marketplace.
- Mass candidate application.
- AI CV writer.
- CV scraping aggregator.
- Global marketplace.
- Junior talent marketplace.
- Automated ranking score cho con người.
- Full payroll hoặc employer-of-record system.

---

## 6. MVP Scope

MVP cần chứng minh một điều:

> Hai agency có sẵn sàng trao đổi verified talent thông qua một workflow chuẩn hay không?

### 6.1. Organization onboarding

- Tạo organization.
- Verify business email.
- Manual company verification.
- Invite organization members.
- Organization role management.

Roles:

- Owner.
- Admin.
- Recruiter.
- Delivery Manager.
- Viewer.

### 6.2. Talent profile

- Tạo talent profile.
- Candidate consent.
- Anonymized public view.
- Full profile chỉ hiện sau khi candidate hoặc agency approve.
- Availability status.
- Rate range.
- Preferred engagement.
- Tech stack.
- Seniority.
- Timezone.
- English proficiency.
- Project evidence.

### 6.3. Talent Passport

- Tạo evidence claim.
- Issuer xác nhận claim.
- Candidate approve visibility.
- Evidence trạng thái pending, verified hoặc rejected.
- Có audit log.

### 6.4. Search and discovery

Filter tối thiểu:

- Role.
- Tech stack.
- Seniority.
- Availability date.
- Rate range.
- Timezone.
- Contract duration.

### 6.5. Introduction workflow

```text
INTRO_REQUESTED
→ CANDIDATE_CONSENT_PENDING
→ INTRO_ACCEPTED
→ DISCOVERY_CALL
→ TECHNICAL_REVIEW
→ COMMERCIAL_NEGOTIATION
→ CONTRACT_PENDING
→ ENGAGED
```

Các trạng thái kết thúc:

```text
REJECTED
WITHDRAWN
EXPIRED
CANCELLED
```

### 6.6. Messaging and notification

- In-app conversation theo từng match.
- Email notification.
- Response deadline.
- Reminder trước khi match hết hạn.

### 6.7. Admin operations

- Organization verification queue.
- Talent verification queue.
- Report handling.
- Fraud review.
- Match intervention.
- Manual status correction.
- Audit log.

### 6.8. MVP không cần

- Native mobile app.
- Real-time chat.
- Video interview.
- Automated matching AI.
- Complex recommendation engine.
- Payroll.
- Escrow.
- Automated legal compliance cho nhiều quốc gia.

---

## 7. Product Roadmap

## Phase 0 — Concierge Validation

Mục tiêu: kiểm tra nhu cầu trước khi build marketplace.

Thời gian mục tiêu: 2–4 tuần.

Hoạt động:

- Phỏng vấn ít nhất 20 agency owners hoặc delivery managers.
- Thu thập danh sách developer đang bench.
- Thu thập nhu cầu talent tạm thời.
- Match thủ công bằng spreadsheet hoặc private database.
- Soạn mẫu talent card tiêu chuẩn.
- Thử nghiệm pricing.

Success criteria:

- Ít nhất 10 agency đồng ý tham gia private network.
- Ít nhất 30 talent profile được cung cấp.
- Ít nhất 10 nhu cầu tuyển hoặc thuê thật.
- Ít nhất 3 introduction.
- Ít nhất 1 transaction hoặc paid pilot.

## Phase 1 — Bench Exchange MVP

Mục tiêu: số hóa workflow đã được xác nhận ở Phase 0.

Tính năng:

- Organization onboarding.
- Talent profile.
- Candidate consent.
- Search.
- Introduction request.
- Match status.
- Basic Passport evidence.
- Admin verification.
- Email notification.

Success criteria:

- 20 verified agencies.
- 100 active talent profiles.
- 20 verified hiring intents hoặc bench requests.
- 10 accepted introductions mỗi tháng.
- 3–5 successful engagements.
- First recurring revenue.

## Phase 2 — Verified Hiring Intent

Mục tiêu: mở demand cho product company và employer ngoài agency network.

Tính năng:

- Hiring intent verification.
- Approved headcount declaration.
- Salary hoặc budget range.
- Response SLA.
- Employer trust profile.
- Membership hoặc deposit.
- Limited candidate access.

Success criteria:

- 80% hiring intents được phản hồi đúng SLA.
- Dưới 10% job bị đóng vì không có nhu cầu thật.
- Ít nhất 20% introductions đi tới technical discussion.
- Ít nhất 5 permanent hoặc contract-to-hire placements.

## Phase 3 — Passport Expansion

Mục tiêu: biến Passport thành tài sản dữ liệu lâu dài.

Tính năng:

- Multiple evidence issuers.
- Project completion evidence.
- Structured reference.
- Trial outcome.
- Contract outcome.
- Candidate-controlled visibility.
- Evidence expiration.
- Export hoặc shareable profile.

Success criteria:

- 50% active talent có ít nhất 3 verified evidence claims.
- Profile có verified evidence đạt conversion cao hơn profile thường.
- Candidate chủ động quay lại cập nhật Passport.

## Phase 4 — Final-Round Candidate Exchange

Mục tiêu: tái sử dụng candidate đã được employer khác đánh giá sâu.

Tính năng:

- Candidate consent workflow.
- Employer-issued structured interview evidence.
- Private final-round pool.
- Referral credit.
- Evidence visibility controls.
- Interview evidence expiration.

Success criteria:

- 30% candidate được mời vào interview mới.
- 10% candidate exchange dẫn tới offer hoặc engagement.
- Employer chủ động đóng góp candidate thay vì chỉ tiêu thụ supply.

## Phase 5 — Network Intelligence

Chỉ triển khai khi đã có đủ transaction data.

Tính năng tiềm năng:

- Evidence-aware matching.
- Match explanation.
- Availability forecasting.
- Rate benchmarking.
- Hiring intent risk detection.
- Fraud detection.
- Agency trust graph.
- Response probability.

AI chỉ là lớp hỗ trợ trên data graph, không phải core value proposition ban đầu.

---

## 8. User Roles

### Candidate

- Quản lý consent.
- Xem và chỉnh sửa Passport.
- Quyết định ai được xem full profile.
- Chấp nhận hoặc từ chối introduction.
- Yêu cầu sửa hoặc thu hồi evidence không chính xác.

### Supplying Agency

- Đăng talent đang bench.
- Xác nhận employment và project evidence.
- Cập nhật availability.
- Đàm phán commercial terms.
- Theo dõi engagement.

### Hiring Agency / Employer

- Tạo verified hiring intent.
- Tìm talent.
- Gửi introduction request.
- Tuân thủ response SLA.
- Cập nhật process outcome.

### Platform Admin

- Verify organization.
- Review evidence.
- Resolve dispute.
- Handle fraud.
- Monitor marketplace health.
- Enforce trust policy.

---

## 9. Core Data Model

```text
Organization
Person
OrganizationMember
TalentProfile
TalentPassport
EvidenceClaim
Availability
HiringIntent
Introduction
MatchProcess
Conversation
Contract
Outcome
ConsentRecord
AuditLog
```

### Organization

```text
id
name
legal_name
website
country
company_type
verification_status
trust_status
created_at
updated_at
```

### Person

```text
id
name
email
identity_verification_status
created_at
updated_at
```

### TalentProfile

```text
id
person_id
current_organization_id
headline
primary_role
seniority
summary
location
timezone
english_level
visibility_status
availability_status
created_at
updated_at
```

### EvidenceClaim

```text
id
passport_id
issuer_organization_id
issuer_person_id
type
title
description
verification_status
visibility
valid_from
expires_at
supporting_artifact_url
created_at
updated_at
```

### Availability

```text
id
talent_profile_id
available_from
available_until
allocation_percent
minimum_duration
maximum_duration
rate_type
rate_min
rate_max
currency
status
```

### HiringIntent

```text
id
organization_id
hiring_manager_id
role_title
role_type
required_skills
seniority
contract_type
approved_headcount
budget_min
budget_max
currency
expected_start_date
interview_process
response_sla_hours
verification_status
deposit_status
status
created_at
updated_at
```

### Introduction

```text
id
hiring_intent_id
talent_profile_id
requesting_organization_id
supplying_organization_id
candidate_consent_status
status
expires_at
created_at
updated_at
```

### Outcome

```text
id
introduction_id
type
reason_code
notes_visibility
started_at
completed_at
created_at
updated_at
```

### ConsentRecord

```text
id
person_id
resource_type
resource_id
consent_type
status
granted_at
revoked_at
metadata
```

---

## 10. System Architecture

Ưu tiên kiến trúc đơn giản, dễ vận hành cho solo founder.

### Recommended architecture

```text
Web Application
    │
    ▼
Modular Monolith API
    │
    ├── PostgreSQL
    ├── Object Storage
    ├── Background Job Queue
    ├── Email Provider
    └── Search Index khi cần
```

### Suggested stack

Không bắt buộc, nhưng phù hợp cho backend-heavy solo founder:

```text
Backend: Go
API: REST hoặc ConnectRPC
Database: PostgreSQL
Cache / Queue: Redis
Frontend: Next.js hoặc React
Authentication: Managed Auth hoặc OIDC
Object Storage: S3-compatible storage
Email: Transactional email provider
Deployment: Managed container platform
Observability: Structured logs, metrics, error tracking
```

### Kiến trúc code

Bắt đầu bằng modular monolith với bounded modules:

```text
/auth
/organizations
/people
/talent
/passports
/evidence
/availability
/hiring-intents
/introductions
/contracts
/outcomes
/consent
/admin
/notifications
```

Không tách microservice trước khi có tải hoặc organizational complexity thực tế.

---

## 11. Core Backend Principles

### 11.1. Auditability

Mọi thay đổi quan trọng phải có audit log:

- Ai thay đổi.
- Thay đổi gì.
- Khi nào.
- Giá trị trước và sau.
- Lý do.

### 11.2. Consent first

Không hiển thị hoặc chia sẻ candidate profile nếu chưa có consent phù hợp.

Consent phải:

- Có scope.
- Có timestamp.
- Có thể revoke.
- Có lịch sử.

### 11.3. Evidence over claims

Profile tự khai và evidence đã xác minh phải được phân biệt rõ.

### 11.4. Privacy by default

Talent profile ở trạng thái anonymized mặc định.

Các thông tin như tên, email, công ty hiện tại và artifact nhạy cảm chỉ được hiện sau khi có quyền.

### 11.5. Human-in-the-loop

Trong giai đoạn đầu:

- Verification thủ công.
- Match review thủ công.
- Fraud review thủ công.
- Dispute resolution thủ công.

Automation chỉ được thêm sau khi workflow đã ổn định.

---

## 12. Trust and Safety

### Các rủi ro chính

- Agency đăng talent mà chưa có consent.
- Profile giả.
- Project evidence phóng đại.
- Employer đăng hiring intent nhưng không có budget.
- Agency bypass platform.
- Candidate bị spam.
- Raw interview notes bị lộ.
- Phân biệt đối xử trong tuyển dụng.
- Tranh chấp quyền sở hữu commercial relationship.

### Các biện pháp ban đầu

- Manual organization verification.
- Business email verification.
- Candidate consent bắt buộc.
- Anonymized profile mặc định.
- Rate limit introduction.
- Hiring intent approval.
- Response SLA.
- Report và dispute workflow.
- Immutable audit events.
- Không cho phép raw interview notes trong Final-Round Exchange.
- Structured reason codes.

---

## 13. Marketplace Rules

### Supply rules

- Talent phải biết mình đang được listed.
- Availability phải được cập nhật định kỳ.
- Agency phải xác nhận relationship với talent.
- Không được giả mạo project evidence.
- Không được list một talent với thông tin mâu thuẫn.

### Demand rules

- Hiring intent phải có người chịu trách nhiệm.
- Phải có budget hoặc compensation range.
- Phải có thời gian bắt đầu dự kiến.
- Phải phản hồi trong SLA.
- Không được mass-message talent.
- Introduction phải liên quan tới intent cụ thể.

### Candidate rules

- Candidate có quyền từ chối introduction.
- Candidate có quyền giới hạn visibility.
- Candidate có quyền revoke consent.
- Candidate có quyền yêu cầu review evidence.

---

## 14. Business Model

### Agency membership

Mức thử nghiệm ban đầu:

```text
$199–499 / tháng / agency
```

Có thể bao gồm:

- Đăng talent đang bench.
- Xem một số lượng profile giới hạn.
- Một số introduction mỗi tháng.
- Verification.
- Standard contract templates.
- Basic analytics.

### Transaction fee

Đối với bench contract:

```text
3–8% contract value
```

Hoặc phí cố định theo mỗi tháng engagement.

### Permanent hiring fee

Có thể thử nghiệm:

- Fixed placement fee.
- 5–10% annual salary.
- Subscription plus reduced placement fee.

### Referral credit

Employer đóng góp final-round candidate có thể nhận:

- Platform credit.
- Discount membership.
- Referral payout theo điều kiện.

---

## 15. Go-To-Market Strategy

Không mở marketplace công khai ngay.

### Bước 1 — Founder-led sales

Target:

- Agency owner.
- CTO.
- Delivery manager.
- Resource manager.
- Head of engineering.

Thông điệp:

> Mày đang có developer bench và vẫn phải trả lương. Tao đang xây private agency network để biến capacity đó thành revenue mà không phải phụ thuộc vào public marketplace.

### Bước 2 — Private founding network

Tạo một founding group gồm 10–20 agency.

Cam kết ban đầu:

- Free hoặc discounted membership.
- Manual matchmaking.
- Influence lên product roadmap.
- Early access.

Đổi lại:

- Cung cấp supply thật.
- Cung cấp demand thật.
- Cập nhật availability.
- Tham gia feedback call.

### Bước 3 — Concierge matching

Trước khi có đủ product:

- Chuẩn hóa talent profile thủ công.
- Gửi curated shortlist.
- Theo dõi introduction bằng CRM hoặc database nội bộ.
- Hỗ trợ negotiation.
- Ghi lại rejection reason.

### Bước 4 — Niche authority

Tập trung content vào:

- Bench utilization.
- Agency resource planning.
- Staff augmentation economics.
- Verified technical talent.
- Remote engineering operations.

Không cạnh tranh bằng lượng traffic với job board.

Cạnh tranh bằng trust và network quality.

---

## 16. North Star Metric

```text
Successful Talent Engagements per Month
```

Một engagement được tính khi:

- Hai bên ký contract hoặc employment agreement.
- Talent bắt đầu làm việc.
- Engagement tồn tại qua một ngưỡng thời gian tối thiểu.

---

## 17. Supporting Metrics

### Supply metrics

- Verified agencies.
- Active talent profiles.
- Talent with current availability.
- Talent with verified evidence.
- Median time since availability update.

### Demand metrics

- Verified hiring intents.
- Intent-to-introduction rate.
- Response SLA compliance.
- Active employer rate.

### Marketplace metrics

- Introduction acceptance rate.
- Interview conversion rate.
- Contract conversion rate.
- Median time to engagement.
- Successful engagements per month.
- Repeat transaction rate.
- Marketplace revenue.

### Trust metrics

- Evidence verification rate.
- Dispute rate.
- Fraud report rate.
- Candidate consent violation rate.
- Employer no-response rate.

---

## 18. Initial Targets

### First 30 days

- 20 customer interviews.
- 10 committed agencies.
- 30 bench profiles.
- 10 active demand requests.
- 3 manual introductions.
- 1 paid transaction hoặc pilot.

### First 90 days

- 20 verified agencies.
- 100 talent profiles.
- 30 active hiring intents hoặc bench requests.
- 20 accepted introductions.
- 5 successful engagements.
- First monthly recurring revenue.

### First 12 months

- 50–100 paying organizations.
- 500–1,000 verified talent profiles.
- 20+ successful engagements mỗi tháng.
- Strong repeat transaction behavior.
- Passport evidence bắt đầu ảnh hưởng rõ tới conversion.

---

## 19. Key Assumptions to Validate

1. Agency sẵn sàng công khai một phần bench capacity cho agency khác.
2. Candidate đồng ý được listed khi vẫn thuộc agency hiện tại.
3. Agency chấp nhận một chuẩn profile chung.
4. Hiring agency sẵn sàng trả membership hoặc transaction fee.
5. Verified evidence cải thiện conversion.
6. Employer chấp nhận response SLA.
7. Final-round candidate exchange không tạo quá nhiều legal hoặc reputational friction.
8. Agency không bypass platform ngay sau introduction.

---

## 20. Main Risks

### Cold-start marketplace

Giảm rủi ro bằng cách:

- Bắt đầu với private founding network.
- Chỉ chọn một segment.
- Concierge matching.
- Không mở self-service quá sớm.

### Agency không muốn chia sẻ talent

Giảm rủi ro bằng:

- Anonymized profiles.
- Candidate consent.
- Controlled visibility.
- Commercial protection.
- Private network.

### Platform bypass

Giảm rủi ro bằng:

- Membership value.
- Verification history.
- Contract workflow.
- Dispute support.
- Passport updates.
- Repeat network access.

Không chỉ dựa vào legal restriction.

### Evidence không đáng tin

Giảm rủi ro bằng:

- Issuer identity.
- Verification tier.
- Audit trail.
- Supporting artifact.
- Reputation của issuer.
- Expiration.

### Scope quá lớn

Giảm rủi ro bằng roadmap tuần tự:

```text
Bench Exchange
→ Basic Passport
→ Verified Hiring Intent
→ Expanded Passport
→ Final-Round Candidate Exchange
```

---

## 21. Product Principles

1. **Trust over volume.**
2. **Transactions over traffic.**
3. **Evidence over self-reported claims.**
4. **Consent over data extraction.**
5. **Private network before public marketplace.**
6. **Manual workflow before automation.**
7. **Narrow vertical before expansion.**
8. **Outcome data before AI matching.**
9. **Candidate dignity over recruiter convenience.**
10. **Liquidity before feature breadth.**

---

## 22. Immediate Execution Plan

### Week 1

- Chốt tên tạm thời và positioning.
- Viết landing page một trang.
- Tạo interview script.
- Lập danh sách 50 software agency mục tiêu.
- Liên hệ 10 agency đầu tiên.

### Week 2

- Hoàn thành ít nhất 10 cuộc phỏng vấn.
- Chuẩn hóa talent profile template.
- Chuẩn hóa hiring intent template.
- Thu thập supply và demand thật.
- Match thủ công lần đầu.

### Week 3

- Thử nghiệm pricing.
- Chốt founding agency agreement.
- Xác định workflow cần số hóa.
- Thiết kế database schema MVP.
- Thiết kế permission và consent model.

### Week 4

- Xây organization onboarding.
- Xây talent profile.
- Xây availability.
- Xây basic search.
- Xây introduction workflow.
- Tiếp tục concierge matching song song.

### Sau tháng đầu

Chỉ build thêm tính năng khi tính năng đó giải quyết một bottleneck đã xuất hiện trong transaction thật.

---

## 23. Definition of Success

Sản phẩm được xem là có tín hiệu tốt khi:

- Agency tự nguyện cập nhật bench availability.
- Agency quay lại tìm talent lần hai.
- Candidate chấp nhận sử dụng Passport.
- Employer tuân thủ response SLA.
- Transaction xảy ra mà founder không phải can thiệp vào mọi bước.
- Verified evidence làm tăng conversion.
- Network tạo ra repeat transaction.

Mục tiêu cuối cùng không phải tạo một job board lớn.

Mục tiêu là tạo một mạng lưới nhỏ nhưng có thanh khoản, trust cao và tạo ra doanh thu từ mỗi interaction chất lượng.

---

## 24. One-Sentence Strategy

> Bán bench utilization trước, tích lũy trust data sau, rồi mới mở rộng sang verified recruitment và final-round candidate exchange.
