namespace TagFilesService.Model;

public class Tag
{
    public Tag(string name)
        : this(0u, name)
    {
    }

    public uint Id { get; private set; }

    public string Name { get; private set; }

    public void Update(string name)
    {
        ValidateName(name);
        Name = name;
    }

    private void ValidateName(string name)
    {
        if (name.Length is 0 or > 50)
        {
            throw new ApplicationException("Tag Name cannot be empty or longer than 50 characters");
        }

        if (!name.All(c => char.IsLetterOrDigit(c) || c == '-'))
        {
            throw new ApplicationException("Tag Name can only contain letters, digits and '-'");
        }
    }

    private Tag(uint id, string name)
    {
        ValidateName(name);
        Name = name;
        Id = id;
    }
}