data "aws_ami" "centos" {
  most_recent = true

  # 679593333241: non-cn regions
  # 336777782633: cn regions, e.g. cn-north-1, cn-northwest-1
  owners = ["679593333241", "336777782633"]

  filter {
    name   = "name"
    values = ["CentOS Linux 7 x86_64 HVM EBS *"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }

  filter {
    name   = "product-code"
    values = ["g3kvn950n45rumoxwlkl2ebw"]
  }

}
