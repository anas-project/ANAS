<?php
/** One read-only source; private SQL contacts remain Roundcube's own source. */
class anas_company_directory extends rcube_addressbook
{
    public $primary_key = 'ID';
    public $readonly = true;
    public $groups = true;
    private $plugin;
    private $filter = [];
    private $result;

    public function __construct($plugin) { $this->plugin=$plugin; $this->ready=true; }
    public function get_name() { return $this->plugin->gettext('company'); }
    public function set_search_set($filter): void { $this->filter=is_array($filter)?array_intersect_key($filter,['query'=>true,'id'=>true]):[]; }
    public function get_search_set() { return $this->filter; }
    public function reset(): void { $this->filter=[]; $this->result=null; }
    private function record($person) {
        if (!$person) return null;
        return ['ID'=>$person['id'],'name'=>$person['name'],'email'=>[$person['email']],
            'phone:work'=>$person['phone']??'', 'phone:mobile'=>$person['mobile']??'',
            'department'=>$person['department']??'', 'jobtitle'=>$person['title']??'',
            'groups'=>$person['groups']??[]];
    }
    private function query($offset,$limit) {
        if (isset($this->filter['id'])) {
            $data=$this->plugin->directory_api('contact',['id'=>(string)$this->filter['id']]);
            return ['records'=>empty($data['record'])?[]:[$data['record']], 'total'=>empty($data['record'])?0:1, 'offset'=>0];
        }
        return $this->plugin->directory_api('search',['query'=>(string)($this->filter['query']??'*'),
            'group'=>(string)($this->group_id?:'all'),'offset'=>(string)$offset,'limit'=>(string)$limit]);
    }
    public function list_records($cols=null,$subset=0,$nocount=false) {
        $limit=max(1,min(100,(int)$this->page_size));$offset=max(0,((int)$this->list_page-1)*$limit);
        $data=$this->query($offset,$limit);$this->result=new rcube_result_set((int)$data['total'],(int)$data['offset']);
        foreach ($data['records'] as $person) $this->result->add($this->record($person));
        return $this->result;
    }
    public function count() { $data=$this->query(0,1); return new rcube_result_set((int)$data['total']); }
    public function get_result() { return $this->result; }
    public function search($fields,$value,$mode=0,$select=true,$nocount=false,$required=[]) {
        $value=is_array($value)?implode(' ',array_filter($value,'is_string')):(string)$value;
        $this->filter=$fields==='ID'?['id'=>$value]:['query'=>trim($value)===''?'*':mb_strcut($value,0,256,'UTF-8')];
        return $this->list_records();
    }
    public function get_record($id,$assoc=false) {
        $data=$this->plugin->directory_api('contact',['id'=>(string)$id]);$record=$this->record($data['record']??null);
        if ($assoc) return $record;
        $result=new rcube_result_set($record?1:0);if ($record) $result->add($record);return $result;
    }
    public function list_groups($search=null,$mode=0) {
        $data=$this->plugin->directory_api('groups');$groups=[];
        foreach ($data['groups'] as $group) if (!$search || mb_stripos($group['name'],$search)!==false) $groups[]=['ID'=>$group['id'],'name'=>$group['name']];
        return $groups;
    }
    public function get_group($id) { foreach($this->list_groups() as $group) if($group['ID']===$id) return $group; return null; }
    public function get_record_groups($id) { $record=$this->get_record($id,true);$result=[];foreach($this->list_groups() as $group) if(in_array($group['ID'],$record['groups']??[],true)) $result[$group['ID']]=$group['name'];return $result; }
    public function set_group($id) { $this->group_id=$id; }
}
